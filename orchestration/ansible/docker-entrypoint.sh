#!/bin/sh
# play.sh runs the container with your user id, which has no entry in the
# image's /etc/passwd, and ssh refuses to run without one.
if ! getent passwd "$(id -u)" >/dev/null 2>&1; then
  echo "barekube:x:$(id -u):$(id -g):barekube:/home/barekube:/bin/sh" >>/etc/passwd
fi
exec ansible-playbook "$@"
