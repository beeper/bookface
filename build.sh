#!/bin/bash
cd "$(dirname "$0")"
go build -o beeper-bookface ./cmd/beeper-bookface
