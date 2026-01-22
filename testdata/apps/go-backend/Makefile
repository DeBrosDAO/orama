.PHONY: build clean run

build:
	GOOS=linux GOARCH=amd64 go build -o api main.go

clean:
	rm -f api

run:
	go run main.go
