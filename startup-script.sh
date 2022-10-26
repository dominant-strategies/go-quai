#! /usr/bin/bash

FUNCTION="rs"

compile() {
  echo -e $'\nCompiling...\n'
  make go-quai
}

run_stats() {
  echo -e '\nRunning stats...\n'
  rm -rf ~/.quai
  rm -rf nodelogs
  make stop
  make run-stats
}

help()
{
  # Display Help
  echo -e "\nHelper script to run go-quai utilities locally.\n"
  echo "Syntax: scriptTemplate [-|h|c|rs]"
  echo "options:"
  echo "-c     Compile go-quai."
  echo "-s    Run stats."
  echo "-h     Print this Help."
  echo
}

while getopts 'csh:' OPTION; do
  case "$OPTION" in
    c)
      compile
      ;;
    s)
      run_stats
      ;;
    -h)
      help
      ;;
    ?)
      echo "OPTION: $OPTION"
      echo "\nscript usage: $(basename \$0) [-h] for help" >&2
      exit 1
      ;;
  esac
done
shift "$(($OPTIND -1))"
