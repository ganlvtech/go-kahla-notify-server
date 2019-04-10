package main

import (
	"log"
	"os"
	"os/signal"
)

const DefaultConfigFile = "config.json"

func main() {
	// Load email and password
	configFile := DefaultConfigFile
	if !fileExists(configFile) {
		err := SaveConfigToFile(configFile, new(Config))
		if err != nil {
			panic(err)
		}
		log.Println("Please input your email and password in:", configFile)
		return
	}
	config, err := LoadConfigFromFile(configFile)
	if err != nil {
		panic(err)
	}

	// Interrupt signal
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Create notify server
	interrupt2 := make(chan struct{})
	go func() {
		<-interrupt
		log.Println("Receive interrupt signal.")
		close(interrupt2)
	}()
	notifyServer := NewNotifyServer(config.Email, config.Password, config.Port)
	err = notifyServer.Run(interrupt2)
	if err != nil {
		log.Println(err)
	}
}
