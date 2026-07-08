#!/bin/bash
set -eu

if [ ! -f docker-compose.yml ]; then
  echo "Error: docker-compose.yml not found" >&2
  exit 1
fi

git pull

shopt -s expand_aliases
alias dcrc='docker compose up --force-recreate -d'

docker compose build
dcrc
