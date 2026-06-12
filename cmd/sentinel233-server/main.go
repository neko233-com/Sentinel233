package main

import (
	"os"

	"github.com/neko233-com/Sentinel233/internal/serverapp"
)

func main() {
	serverapp.Run(os.Args[1:])
}
