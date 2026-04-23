#pc机
#GOOS=linux GOARCH=amd64 go build -o hello_linux main.go
#一体机
#  haantslq
# ./generate_release_readme.sh
GOOS=linux GOARCH=arm64 go build -o play-from-disk-h264-arm main.go
# GOOS=linux CGO_ENABLED=1 GOARCH=arm64 go build -o valut-service-arm main.go
#  CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc go build -o valut-service-arm main.go

# scp valut-service-arm root@192.168.2.49:/home/aland-service/tmp
scp play-from-disk-h264-arm output.h264 run.sh root@192.168.88.56:/tmp/
# scp valut-service-arm root@200.242.142.10:/tmp