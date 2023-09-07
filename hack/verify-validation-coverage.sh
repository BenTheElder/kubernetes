#!/usr/bin/env bash

source hack/lib/init.sh
kube::golang::setup_env

go run hack/coverage/check_coverage.go
