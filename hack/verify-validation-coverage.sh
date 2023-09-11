#!/usr/bin/env bash

source hack/lib/init.sh
kube::golang::setup_env

time go run ./hack/validation-coverage ./api/openapi-spec/v3/ ./pkg/apis
