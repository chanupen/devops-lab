#!/bin/sh
# Answers one HTTP request: read the request line, reply with plain text.
read -r _request
body="hello from lab 2"
printf 'HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s\n' "$((${#body} + 1))" "$body"
