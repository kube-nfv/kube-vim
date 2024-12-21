package config

import "errors"

var (
	UnsupportedErr     = errors.New("unsupported")
	NotImplementedErr  = errors.New("not implemented")
	NotFoundErr        = errors.New("not found")
    InvalidArgumentErr = errors.New("invalid argument")
)
