#!/bin/sh
set -e

: "${BASE_URL:=}"
: "${API_PATH:=/api}"

sed \
  -e "s|__BASE_URL__|${BASE_URL}|g" \
  -e "s|__API_PATH__|${API_PATH}|g" \
  /usr/share/nginx/html/index.template.html \
  > /usr/share/nginx/html/index.html

exec nginx -g 'daemon off;'
