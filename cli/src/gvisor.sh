#!/bin/bash

GVISOR_KEY_URL="https://gvisor.dev/archive.key"
GVISOR_KEYRING="/usr/share/keyrings/gvisor-archive-keyring.gpg"
GVISOR_REPO_URL="https://storage.googleapis.com/gvisor/releases"
GVISOR_APT_SOURCE="/etc/apt/sources.list.d/gvisor.list"
K3S_CONTAINERD_DIR="/var/lib/rancher/k3s/agent/etc/containerd"
# k3s applies every manifest in this directory on the server nodes, and removes
# nothing when a file is deleted. The files below belong to barekube.
K3S_MANIFESTS_DIR="/var/lib/rancher/k3s/server/manifests"
GVISOR_RUNTIMECLASSES_MANIFEST="$K3S_MANIFESTS_DIR/barekube-gvisor.yaml"
GVISOR_DEFAULT_MANIFEST="$K3S_MANIFESTS_DIR/barekube-gvisor-default.yaml"

install_gvisor_prerequisites() {
    run_privileged "Installing gVisor dependencies..." "Failed to install gVisor dependencies" \
        apt install -y apt-transport-https ca-certificates curl gnupg
}

add_gvisor_apt_key() {
    echo_message "Adding gVisor key..."
    curl -fsSL "$GVISOR_KEY_URL" | sudo gpg --dearmor --yes -o "$GVISOR_KEYRING"
    exit_if_error "Failed to add gVisor key"
}

add_gvisor_apt_repository() {
    echo_message "Adding gVisor repository..."
    echo "deb [arch=$(dpkg --print-architecture) signed-by=$GVISOR_KEYRING] $GVISOR_REPO_URL release main" \
        | sudo tee "$GVISOR_APT_SOURCE" > /dev/null
    exit_if_error "Failed to add gVisor repository"
}

install_gvisor_package() {
    echo_message "Installing gVisor..."
    sudo apt-get update && sudo apt-get install -y runsc
    exit_if_error "Failed to install gVisor"
}

install_gvisor() {
    install_gvisor_prerequisites
    add_gvisor_apt_key
    add_gvisor_apt_repository
    install_gvisor_package
}

# The runtime stanza for containerd 1.x (config version 2), which older k3s uses.
runsc_runtime_config_v2() {
    cat <<'EOF'
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
EOF
}

# The same for containerd 2.x (config version 3), which current k3s uses. The
# CRI plugin has a different name there; the version 2 stanza is silently
# ignored by it, which leaves no runsc runtime registered.
runsc_runtime_config_v3() {
    cat <<'EOF'
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc]
  runtime_type = 'io.containerd.runsc.v1'
EOF
}

# Adds the runsc runtime to one k3s containerd template. A new template extends
# the configuration k3s generates through its base template, so it keeps
# working as k3s changes its defaults; an existing template is only appended to.
# Returns 0 when it changed the template, 1 when runsc was already in it.
# Usage: add_runsc_to_template FILE STANZA_FUNCTION
add_runsc_to_template() {
    local FILE="$1" STANZA="$2"
    sudo grep -qs ".containerd.runtimes.runsc" "$FILE" && return 1
    if ! sudo test -f "$FILE"
    then
        echo '{{ template "base" . }}' | sudo tee "$FILE" > /dev/null
        exit_if_error "Failed to create the containerd template $FILE"
    fi
    echo_info "Adding runsc runtime to $FILE..."
    "$STANZA" | sudo tee -a "$FILE" > /dev/null
    exit_if_error "Failed to add runsc runtime to containerd configuration"
    return 0
}

# The RuntimeClasses pods use to pick a runtime: gvisor for the sandbox, runc
# to stay out of it.
gvisor_runtime_classes_manifest() {
    cat <<'EOF'
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: gvisor
handler: runsc
---
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: runc
handler: runc
EOF
}

# Sets runtimeClassName: gvisor on every new pod that names no RuntimeClass.
# Pods that need the host (host namespaces, privileged containers) and the
# system namespaces are left alone: gVisor cannot run them.
gvisor_default_policy_manifest() {
    cat <<'EOF'
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicy
metadata:
  name: barekube-gvisor-default
spec:
  failurePolicy: Fail
  reinvocationPolicy: Never
  matchConstraints:
    resourceRules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE"]
        resources: ["pods"]
  matchConditions:
    - name: names-no-runtime-class
      expression: "!has(object.spec.runtimeClassName)"
    - name: does-not-use-host-namespaces
      expression: >-
        !(has(object.spec.hostNetwork) && object.spec.hostNetwork) &&
        !(has(object.spec.hostPID) && object.spec.hostPID) &&
        !(has(object.spec.hostIPC) && object.spec.hostIPC)
    - name: runs-no-privileged-container
      expression: >-
        !(object.spec.containers + (has(object.spec.initContainers) ? object.spec.initContainers : []))
        .exists(c, has(c.securityContext) && has(c.securityContext.privileged) && c.securityContext.privileged)
  mutations:
    - patchType: JSONPatch
      jsonPatch:
        expression: >-
          [JSONPatch{op: "add", path: "/spec/runtimeClassName", value: "gvisor"}]
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingAdmissionPolicyBinding
metadata:
  name: barekube-gvisor-default
spec:
  policyName: barekube-gvisor-default
  matchResources:
    namespaceSelector:
      matchExpressions:
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values: [kube-system, kube-public, kube-node-lease, longhorn-system]
EOF
}

# Writes a manifest for k3s to apply.
# Returns 0 when it wrote the file, 1 when the file was already as it should be.
# Usage: write_k3s_manifest FILE CONTENT_FUNCTION
write_k3s_manifest() {
    local FILE="$1" CONTENT="$2"
    "$CONTENT" | sudo cmp -s - "$FILE" && return 1
    run_privileged "Preparing $K3S_MANIFESTS_DIR..." "Failed to create $K3S_MANIFESTS_DIR" \
        mkdir -p "$K3S_MANIFESTS_DIR"
    echo_info "Writing $FILE..."
    "$CONTENT" | sudo tee "$FILE" > /dev/null
    exit_if_error "Failed to write $FILE"
    return 0
}

# Returns 0 when it removed the manifest, 1 when there was none.
# Usage: remove_k3s_manifest FILE
remove_k3s_manifest() {
    sudo test -f "$1" || return 1
    echo_info "Removing $1..."
    sudo rm -f "$1"
    exit_if_error "Failed to remove $1"
    return 0
}

# Registers the runsc runtime with containerd on this node; k3s restarts once
# if anything changed.
register_gvisor_to_k3s() {
    local SERVICE CHANGED=1
    SERVICE="$(k3s_service_name)"
    echo_message "Checking containerd configuration..."
    run_privileged "Preparing the containerd template directory..." "Failed to create $K3S_CONTAINERD_DIR" \
        mkdir -p "$K3S_CONTAINERD_DIR"

    add_runsc_to_template "$K3S_CONTAINERD_DIR/config.toml.tmpl" runsc_runtime_config_v2 && CHANGED=0
    add_runsc_to_template "$K3S_CONTAINERD_DIR/config-v3.toml.tmpl" runsc_runtime_config_v3 && CHANGED=0
    if [[ "$CHANGED" -ne 0 ]]
    then
        echo_message "The runsc runtime is already registered."
        return 0
    fi

    run_privileged "Restarting $SERVICE..." "Failed to register gVisor to k3s" systemctl restart "$SERVICE"
}

# Makes the gvisor and runc RuntimeClasses available in the cluster and, when
# asked, makes gVisor the default for pods that name no RuntimeClass. These are
# cluster objects, applied by a server node; agents have nothing to do here.
# Usage: publish_gvisor_to_cluster [on|off]   (empty leaves the default as it is)
publish_gvisor_to_cluster() {
    if [[ "$(k3s_service_name)" != "k3s" ]]
    then
        if [[ -n "$1" ]]
        then
            echo_error "The gVisor default is a cluster setting: run this on a control-plane node, not an agent."
            return 1
        fi
        return 0
    fi
    write_k3s_manifest "$GVISOR_RUNTIMECLASSES_MANIFEST" gvisor_runtime_classes_manifest
    case "$1" in
        on) write_k3s_manifest "$GVISOR_DEFAULT_MANIFEST" gvisor_default_policy_manifest ;;
        off)
            # k3s does not delete what a removed manifest created.
            remove_k3s_manifest "$GVISOR_DEFAULT_MANIFEST"
            run_privileged "Removing the gVisor default policy from the cluster..." \
                "Failed to remove the gVisor default policy" \
                k3s kubectl delete mutatingadmissionpolicybinding,mutatingadmissionpolicy \
                barekube-gvisor-default --ignore-not-found
            # k3s remembers the manifest by its checksum and would not apply the
            # same file again when the default is switched back on.
            run_privileged "Forgetting the gVisor default policy manifest..." \
                "Failed to forget the gVisor default policy manifest" \
                k3s kubectl --namespace kube-system delete addons.k3s.cattle.io \
                barekube-gvisor-default --ignore-not-found
            ;;
    esac
    return 0
}

# Installs gVisor and wires it into k3s and the cluster - what every node install needs.
# Usage: setup_gvisor [on|off]
setup_gvisor() {
    install_gvisor
    register_gvisor_to_k3s
    publish_gvisor_to_cluster "$1" || exit 1
}
