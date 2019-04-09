# Go Kahla Notify Server

This project is not related to [`go-kahla-notify`](https://github.com/ganlvtech/go-kahla-notify).

* `go-kahla-notify` is a client program, runs on your computer and notify you when you receive message.

* `go-kahla-notify-server` is a server program, runs on someone's server. Your application sends a request to `go-kahla-notify-server`. Then, it sends a request to Kahla's server. Kahla notifies you. And you will get the notification by `go-kahla-notify`. In short, it works like telegram.

Your application -> `go-kahla-notify-server` -> Kahla server -> Stargate -> Your computer -> `go-kahla-notify`

## Usage

1. Send friend request to `Kahla Notify Bot`

2. It will accept your friend request immediately and reply a message. It is your access token.

3. Send a notification using an HTTP GET request like `/send?token=0123456789abcdef0123456789abcdef&message=HelloWorld`.

4. You can reset your access token by sending `Kahla Notify Bot` a message `reset access token`.

5. You can delete your account from `go-kahla-notify-server` by delete the friend `Kahla Notify Bot`.

## Deploy

1. Run `./go-kahla-notify-server` to generate a `config.json`.

2. Edit `config.json`.

3. Run `./go-kahla-notify-server`.gi

## [MIT License](LICENSE)
