# Lab 2's app: a tiny HTTP server on busybox nc. The base image is old on
# purpose: alpine 3.18.0 has HIGH and CRITICAL CVEs (busybox, OpenSSL, musl).
# Step 3 moves it to a patched base.
FROM docker.io/library/alpine:3.18.0@sha256:02bb6f428431fbc2809c5d1b41eab5a68350194fb508869a33cb1af4444c9b11
COPY serve.sh /app/serve.sh
RUN chmod 0555 /app/serve.sh && adduser -D -H -u 10001 app
USER 10001
EXPOSE 8080
# -l listen, -k keep listening after each connection, -e run serve.sh per request.
CMD ["nc", "-lk", "-p", "8080", "-e", "/app/serve.sh"]
