package configs

//go:generate ../bin/oapi-codegen -package=config -generate=types,skip-prune -o ../internal/config/kubevim_types.go kube-vim-config.openapi.yaml
