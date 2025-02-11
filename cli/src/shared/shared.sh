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

function question(){
    local MESSAGE
    MESSAGE=$(green_bold "[$(timestamp)] $1\n")  
    read -sn 1 -r -p "$MESSAGE" ANSWER
    # shellcheck disable=SC2028
    echo  "${ANSWER//[$'\t\r\n ']}"
}

function read_input(){
    local MESSAGE
    MESSAGE=$(green_bold "[$(timestamp)] $1\n")  
    read -r -p "$MESSAGE" ANSWER
    # shellcheck disable=SC2028
    echo  "${ANSWER//[$'\t\r\n ']}"
}

function exit_if_error(){
    if [ "$?" -ne "0" ]
    then
        echo_error "$1"
        exit $?
    fi
}

function echo_final(){
    if [ "$?" -ne "0" ]
    then
        echo_error "$1"
        exit $?
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
    if [[ "$ANSWER" = "y" || "$ANSWER" = "Y" ]]
    then
        return 0
    else
        return 1
    fi
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
    if [ $? -ne 0 ]
    then
        return 1
    else
        return 0
    fi
}
