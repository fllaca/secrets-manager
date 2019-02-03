#!/bin/sh
# Manually remove third party dependencies that conflict with the actual project deps.
# See https://github.com/golang/go/issues/12432#issuecomment-138792373 to find a more 
# detailed explanation on why this is needed.
# Also: https://github.com/Masterminds/glide/issues/73
rm -rf vendor/k8s.io/apiextensions-apiserver/vendor
