#!/bin/bash

set -e

# Set the maximum number of retries
MAX_RETRIES=3

# Define a function to run a command and check the return code
# The function takes two arguments: the command to run and a description of the command
function run_command() {
  local cmd=$1
  local retries=0
  local retry_cmd="$cmd"
  while true; do
    # Run the command and capture the return code
    $retry_cmd 2>&1 | tee /tmp/install.log || true
    local ret=$?
    # If the command succeeded, break out of the loop
    if [[ $ret -eq 0 ]]; then
      break
    fi
    # If the command failed and we have retries left, print a warning and retry
    if [[ $retries -lt $MAX_RETRIES ]]; then
      retries=$((retries + 1))
      echo "WARNING: $cmd failed with return code $ret. Retrying ($retries/$MAX_RETRIES)..."
      # If the error message indicates missing packages, retry with --fix-missing
      if [[ $cmd == "apt-get update"* ]] && grep -q 'Failed to fetch' /tmp/install.log; then
        retry_cmd="apt-get update -y --fix-missing"
      elif [[ $cmd == "apt-get install"* ]] &&  grep -q 'Unable to fetch some archives' /tmp/install.log; then
        retry_cmd="apt-get install -y --fix-missing $package_name"
      fi
    else
      # If we've exhausted all retries, exit with an error
      echo "ERROR: $cmd failed with return code $ret after $MAX_RETRIES retries."
      exit 1
    fi
  done
}

# Run apt-get update and check the return code
run_command "apt-get update -y" 
package_name=$@
# Run apt-get install and check the return code
run_command "apt-get install -y $package_name"
