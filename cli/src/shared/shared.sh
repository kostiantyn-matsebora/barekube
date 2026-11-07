#!/bin/bash

timestamp() {
  date +%F_%T # current date and time
}

# Prints "[timestamp] MESSAGE" in the colour given by a colors.sh function.
log_line() {
    local COLOR="$1"
    echo -e "$("$COLOR" "[$(timestamp)] $2\n")"
}

echo_message(){
    log_line green "$1"
}

echo_info(){
    log_line yellow_bold "$1"
}

echo_error(){
    log_line red "$1"
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

# Prints a plain, stable state line ("installed", "already installed") that
# tools driving the CLI can match on.
function report_state(){
    echo "$1"
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

function is_automatic_mode(){
    if [[ "$1" == "--yes" || "$1" == "-y" ]]
    then
        echo 0
    else
        echo 1
    fi
}

function check_if_command_exists(){
    command -v "$1" &> /dev/null
}
