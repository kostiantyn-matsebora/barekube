#!/bin/bash

timestamp() {
  date +%F_%T # current date and time
}

green() {
    echo -e "\033[0;32m$1\033[0m"
}

yellow_bold() {
    echo -e "\033[1;33m$1\033[0m"
}

red() {
    echo -e "\033[0;31m$1\033[0m"
}

green_bold() {
    echo -e "\033[1;92m$1\033[0m"
}

magenta() {
    echo -e "\033[0;35m$1\033[0m"
}

echo_message(){
    echo -e "$(green "[$(timestamp)] $1\n")"
}

echo_info(){
    echo -e "$(yellow_bold "[$(timestamp)] $1\n")"

}

echo_important(){
    echo -e "$(magenta "[$(timestamp)] $1\n")"

}

echo_error(){
    echo -e "$(red "[$(timestamp)] $1\n")"
}

function prompt_message(){
    green_bold "[$(timestamp)] $1\n"
}

function remove_whitespace(){
    echo "${1//[$'\t\r\n ']}"
}

function question(){
    local ANSWER
    read -sn 1 -r -p "$(prompt_message "$1")" ANSWER
    remove_whitespace "$ANSWER"
}

function read_input(){
    local ANSWER
    read -r -p "$(prompt_message "$1")" ANSWER
    remove_whitespace "$ANSWER"
}

# Reads a value; reports ERROR_MESSAGE on stderr and fails when empty.
function read_required_input(){
    local VALUE
    VALUE=$(read_input "$1")
    if [[ -z "$VALUE" ]]
    then
        echo_error "$2" >&2
        return 1
    fi
    echo "$VALUE"
}

# Runs "sudo COMMAND..." announcing PROGRESS and stopping with FAILURE on error.
function run_privileged(){
    local PROGRESS="$1" FAILURE="$2"
    shift 2
    echo_message "$PROGRESS"
    sudo "$@"
    exit_if_error "$FAILURE"
}

function exit_if_error(){
    local STATUS=$?
    if [ "$STATUS" -ne "0" ]
    then
        echo_error "$1"
        exit "$STATUS"
    fi
}

function echo_final(){
    local STATUS=$?
    if [ "$STATUS" -ne "0" ]
    then
        echo_error "$1"
        exit "$STATUS"
    else 
        echo_info "$2"
    fi
}

function yes_or_no(){
    if [[ "$2" == "0" ]]
    then
        return 0
    fi

    local ANSWER
    ANSWER=$(question "$1 [y/n]")
    [[ "$ANSWER" == [yY] ]]
}

function exit_if_answer_no() {
    if [[ $? -ne 0 ]]
    then
        echo_message "Exiting..."
        exit 0
    fi
}

function is_automatic_mode(){
    if [[ "$1" == "--yes" || "$1" == "-y" ]]
    then
        echo 0
    else
        echo 1
    fi
}

function press_any_key(){
    read -n 1 -s -r -p "Press any key to continue..."
}

function check_if_command_exists(){
    command -v "$1" &> /dev/null
}
