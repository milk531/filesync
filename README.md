Filesync
===
Filesync is an utility written in Golang which helps you to keep the files on the client up to date with the files on the server. Only the changed parts of files on the server are downloaded. Therefore it's great to synchronize your huge, and frequently changing files.

Server
===
Installation
---

`go get -u github.com/elgs/filesync/gsyncd`

Run
---
`gsyncd gsyncd.json`

Configuration
---
gsyncd.json
```json
{
    "ip": "0.0.0.0",
    "port": 6776,
    "priKeyFile":"private_key.pem",   //private key file to decryption query
    "monitors": {
        "AUTH_KEY_1": "monitored_dir_1",  //one auth key to one dir path
        "AUTH_KEY_2": "monitored_dir_2"
    }
}
```


Client
===
Installtion
---

`go get github.com/elgs/filesync/gsync`

Run
---
`gsync gsync.json`

Configuration
---
gsync.json
```json
{
    "ip": "0.0.0.0",
    "port": 6776,
    "maxScanInterval": "5m",   //max scan interval time to detect files changes on server
    "pubKeyFile":"public.key", //public key file to encryption query
    "monitors": {
        "AUTH_KEY_1": "monitored_dir_1",   //one auth key to one dir path
        "AUTH_KEY_2": "monitored_dir_2"
    }
}
```
