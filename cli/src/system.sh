#!/bin/bash

update_packages() {
    run_privileged "Updating packages..." "Failed to update packages" apt update
}

install_wireguard() {
    run_privileged "Installing wireguard..." "Failed to install wireguard" apt install -y wireguard
}

enable_autoupdate() {
  echo_message "Enabling automatic updates of system packages..."
  sudo apt update
  sudo apt install -y unattended-upgrades
  sudo dpkg-reconfigure --priority=low unattended-upgrades
  echo_message "Automatic updates enabled."
  sudo nano /etc/apt/apt.conf.d/50unattended-upgrades
}
