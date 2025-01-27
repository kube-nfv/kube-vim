// Package config provides primitives to interact with the openapi HTTP API.
//
// Code generated by github.com/oapi-codegen/oapi-codegen/v2 version v2.4.0 DO NOT EDIT.
package config

import (
	externalRef0 "github.com/kube-nfv/kube-vim/internal/config"
)

// Config Top-level configuration node for kube-vim gateway.
type Config struct {
	// Kubevim Kube-vim connection configuration.
	Kubevim *KubeVimConfig `json:"kubevim,omitempty"`

	// Service Configuration related to the kube-vim Gateway service.
	Service *ServiceConfig `json:"service,omitempty"`
}

// KubeVimConfig Kube-vim connection configuration.
type KubeVimConfig struct {
	// Ip "IP address in either IPv4 or IPv6 format. An IP address is used to uniquely identify
	// a device on a network. This schema can accept both IPv4 addresses (e.g., '192.168.0.1')
	// and IPv6 addresses (e.g., '2001:0db8:85a3:0000:0000:8a2e:0370:7334').
	//
	// The address must be in a valid format according to the respective version. IPv4 addresses
	// are written as four decimal numbers (each between 0 and 255) separated by periods (e.g., '192.168.1.1').
	// IPv6 addresses are written as eight groups of four hexadecimal digits, separated by colons (e.g., '2001:db8::ff00:42:8329').
	//
	// The format will be validated to ensure the correct syntax for either version."
	Ip *externalRef0.IpAddress `json:"ip,omitempty"`

	// Port "A TCP port number specifies the endpoint for network communication on the service.
	// Port numbers range from 1 to 65535, with the lower range (1-1023) typically reserved for well-known services and system processes.
	// It is important to choose a port within the allowed range that does not conflict with other services running on the host.
	//
	// Ensure that the selected port is open and accessible for communication while respecting the security policies of your network.
	// Avoid using ports that are commonly blocked by firewalls or reserved for specific applications."
	Port *externalRef0.Port `json:"port,omitempty"`

	// Tls "TLS client configuration defines the settings required to establish a secure
	// connection to a server using TLS. It controls aspects such as certificate validation,
	// server verification, and root certificate authorities used in the validation process."
	Tls *externalRef0.TlsClientConfig `json:"tls,omitempty"`
}

// ServerConfig Kube-vim Gateway Server configuration.
type ServerConfig struct {
	// Ip "IP address in either IPv4 or IPv6 format. An IP address is used to uniquely identify
	// a device on a network. This schema can accept both IPv4 addresses (e.g., '192.168.0.1')
	// and IPv6 addresses (e.g., '2001:0db8:85a3:0000:0000:8a2e:0370:7334').
	//
	// The address must be in a valid format according to the respective version. IPv4 addresses
	// are written as four decimal numbers (each between 0 and 255) separated by periods (e.g., '192.168.1.1').
	// IPv6 addresses are written as eight groups of four hexadecimal digits, separated by colons (e.g., '2001:db8::ff00:42:8329').
	//
	// The format will be validated to ensure the correct syntax for either version."
	Ip *externalRef0.IpAddress `json:"ip,omitempty"`

	// Port "A TCP port number specifies the endpoint for network communication on the service.
	// Port numbers range from 1 to 65535, with the lower range (1-1023) typically reserved for well-known services and system processes.
	// It is important to choose a port within the allowed range that does not conflict with other services running on the host.
	//
	// Ensure that the selected port is open and accessible for communication while respecting the security policies of your network.
	// Avoid using ports that are commonly blocked by firewalls or reserved for specific applications."
	Port *externalRef0.Port `json:"port,omitempty"`

	// Tls "TLS server configuration defines the settings for establishing secure TLS communication on the server-side. This configuration
	// ensures that the server can securely encrypt data with clients, protecting against eavesdropping, tampering, and forgery.
	// The configuration includes settings for the server's x.509 certificate and private key, which are essential for the TLS handshake."
	Tls *externalRef0.TlsServerConfig `json:"tls,omitempty"`
}

// ServiceConfig Configuration related to the kube-vim Gateway service.
type ServiceConfig struct {
	// LogLevel "Log level defines the severity of messages that will be recorded in the application's
	// logs. The available log levels are:
	// - 'trace': Provides the most detailed logging, often used for debugging specific issues.
	// - 'debug': Includes less verbose logging than 'trace', typically for development and debugging purposes.
	// - 'info': Standard logging level that provides general application flow and operational information.
	// - 'warn': Logs warnings that highlight potential issues or unexpected conditions that do not necessarily indicate a failure.
	// - 'error': Logs errors that indicate a failure or significant issue in the application.
	//
	// The default value is 'info', which strikes a balance between verbosity and usefulness for most environments."
	LogLevel *externalRef0.LogLevel `json:"logLevel,omitempty"`

	// Server Kube-vim Gateway Server configuration.
	Server *ServerConfig `json:"server,omitempty"`
}
