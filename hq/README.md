The `hq` tool is extremely limited, but it can exercise basic client and server
capabilities.

Setup:

```
.../minhq $ cd ./hq
.../hq $ go build .
.../hq $ ./makecert.sh
```

Start a server:

```
.../hq $ ./hq server localhost:8443 cert.pem key.pem
:authority: localhost:8443
     :path: /testing
   :method: GET
   :scheme: https
[[[
GET / HTTP/1.1
Host: enabled.tls13.com
Connection: close


]]]
```

Poke it with a client:

```
.../hq $ ./hq/hq client https://localhost:8443/test
:status: 200
 server: hq

[[[
:authority: localhost:8443
     :path: /testing
   :method: GET
   :scheme: https

[[[
GET / HTTP/1.1
Host: enabled.tls13.com
Connection: close


]]]
]]]
```
