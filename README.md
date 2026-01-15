# beeper-bookface

A Matrix-Bookface puppeting bridge for Y Combinator's messaging platform.

## Building

```bash
./build.sh
```

## Running with Beeper

```bash
bbctl -e staging run --type bridgev2 --custom-startup-command "$(pwd)/beeper-bookface" sh-bookface
```

## Authentication

The bridge uses YC SSO authentication. Log in with your YC username (YCID) and password.

## Features & Roadmap

[ROADMAP.md](ROADMAP.md) contains a general overview of what is supported by the bridge.
