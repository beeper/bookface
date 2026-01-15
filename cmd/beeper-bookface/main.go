package main

import (
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	"github.com/beeper/bookface/pkg/connector"
)

// Information to find out exactly which commit the bridge was built from.
// These are filled at build time with the -X linker flag.
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	m := mxmain.BridgeMain{
		Name:        "beeper-bookface",
		URL:         "https://github.com/beeper/bookface",
		Description: "A Matrix-Bookface puppeting bridge",
		Version:     "0.1.0",
		Connector:   &connector.BookfaceConnector{},
	}
	m.InitVersion(Tag, Commit, BuildTime)
	m.Run()
}
