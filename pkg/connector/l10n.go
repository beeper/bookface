package connector

import (
	"go.mau.fi/util/l10n"
)

var L = l10n.NewNamespace("bookface")

var (
	msgFlowPasswordName        = L.Define("login.flow.password.name", "Password")
	msgFlowPasswordDescription = L.Define("login.flow.password.description", "Log in with your YC username and password")

	msgStepPasswordInstructions = L.Define("login.step.password.instructions", "Enter your YC (Y Combinator) login credentials. These are the same credentials you use to log into Bookface or other YC services.")
	msgStepCompleteInstructions = L.Define("login.step.complete.instructions", "Successfully logged in as {name}",
		l10n.WithComment("name is the display name of the YC account"))

	msgFieldYCIDName            = L.Define("login.field.ycid.name", "YC ID")
	msgFieldYCIDDescription     = L.Define("login.field.ycid.description", "Your YC username (e.g., your Bookface login)")
	msgFieldPasswordName        = L.Define("login.field.password.name", "Password")
	msgFieldPasswordDescription = L.Define("login.field.password.description", "Your YC account password")
)
