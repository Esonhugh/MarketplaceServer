package main

import (
	"context"
	"os"

	"github.com/Esonhugh/MarketplaceServer/cmd"
	gitmod "github.com/Esonhugh/MarketplaceServer/mod/git"
)

func main() {
	if _, ok := gitmod.ProtectedReceiveHookMode(); ok {
		if err := gitmod.RunProtectedReceiveHook(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	cmd.Execute()
}
