package main

import (
	"fmt"

	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
)

func main() {
	fmt.Println(buildinfo.ForService("control-plane"))
}
