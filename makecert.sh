#!/bin/bash
ec=(-newkey ec -pkeyopt ec_paramgen_curve:prime256v1)
rsa=(-newkey rsa:1024)
names=(DNS:localhost DNS:example.com DNS:www.example.com)
exec openssl req -new "${ec[@]}" \
    -days 365 \
    -nodes \
    -x509 \
    -subj "/CN=${names[0]}" \
    -extensions SAN \
    -config <(cat /etc/ssl/openssl.cnf; \
        echo "[SAN]"; \
        IFS=,;echo "subjectAltName=${names[*]}") \
    -verbose \
    -keyout key.pem \
    -out cert.pem
