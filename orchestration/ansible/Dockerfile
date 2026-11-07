# Everything the playbooks need, so nothing has to be installed on your machine
# except Docker: Ansible, the collections, an SSH client and cosign (which
# checks the signature of the barekube release).
FROM python:3.13-slim

COPY --from=gcr.io/projectsigstore/cosign:v2.5.0 /ko-app/cosign /usr/local/bin/cosign

RUN apt-get update \
    && apt-get install -y --no-install-recommends openssh-client \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt requirements.yml /tmp/
RUN pip install --no-cache-dir -r /tmp/requirements.txt \
    && ansible-galaxy collection install -r /tmp/requirements.yml --collections-path /usr/share/ansible/collections

# play.sh runs as your user id: give it a home directory and a passwd entry.
RUN mkdir -p /home/barekube && chmod 777 /home/barekube && chmod 666 /etc/passwd
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

WORKDIR /ansible
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
