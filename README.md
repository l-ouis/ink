# ink

personal website engine


```sh
go build -o ink .
./ink passwd            # admin passwd
./ink serve             # :8080 ./content
```

sign in at `/admin/login`.


```
ink serve [-addr :8080] [-content content] [-config data/config.json]
ink passwd [-config data/config.json]
```

