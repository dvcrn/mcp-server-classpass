package main

import (
	"context"
	"log"
	"os"

	"github.com/dvcrn/mcp-server-classpass/classpass"
	"github.com/dvcrn/mcp-server-classpass/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	token := os.Getenv("CLASSPASS_TOKEN")
	user, pass := os.Getenv("CLASSPASS_USERNAME"), os.Getenv("CLASSPASS_PASSWORD")

	if token == "" && (user == "" || pass == "") {
		log.Fatal("set CLASSPASS_TOKEN or CLASSPASS_USERNAME and CLASSPASS_PASSWORD")
	}

	s := server.New(classpass.New(token, user, pass))
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
