package main

import (
	"brainbird-backend/server"
	"log"
)

func main() {
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
