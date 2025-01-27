package configs

//go:generate ../bin/oapi-codegen -package=common -generate=types,skip-prune -o ../internal/config/common_types.go common.openapi.yaml
//go:fmt ../internal/config/common_types.go

//go:generate mkdir -p ../internal/config/kubevim
//go:generate ../bin/oapi-codegen -package=config -generate=types,skip-prune -o ../internal/config/kubevim/gen.go --import-mapping ./common.openapi.yaml:"github.com/kube-nfv/kube-vim/internal/config" kube-vim-config.openapi.yaml
//go:fmt ../internal/config/kubevim/gen.go

//go:generate mkdir -p ../internal/config/gateway
//go:generate ../bin/oapi-codegen -package=config -generate=types,skip-prune -o ../internal/config/gateway/gen.go --import-mapping ./common.openapi.yaml:"github.com/kube-nfv/kube-vim/internal/config" kube-vim-gw-config.openapi.yaml
//go:fmt ../internal/config/gateway/gen.go
