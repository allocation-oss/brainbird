package main

import (
	"log"

	"brainbird-backend/server"
)

func main() {
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
