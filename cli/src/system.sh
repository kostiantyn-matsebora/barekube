#!/bin/bash

enable_autoupdate() {
  echo_message "Enabling automatic updates of system packages..."
  sudo apt update
  sudo apt install -y unattended-upgrades
  sudo dpkg-reconfigure --priority=low unattended-upgrades
  echo_message "Automatic updates enabled."
  sudo nano /etc/apt/apt.conf.d/50unattended-upgrades
}
