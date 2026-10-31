package http

import (
	"context"
	"testing"

	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func httpSource(h *imagev1.HttpSource) *imagev1.ImageSource {
	return &imagev1.ImageSource{Source: &imagev1.ImageSource_Http{Http: h}}
}

func TestImportSource(t *testing.T) {
	src := httpSource(&imagev1.HttpSource{Url: "http://example.com/a.qcow2", ExtraHeaders: []string{"Authorization: x"}})
	got, err := New().ImportSource(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, "http://example.com/a.qcow2", got.HTTP.URL)
	assert.Equal(t, []string{"Authorization: x"}, got.HTTP.ExtraHeaders)

	unsupported := map[string]*imagev1.HttpSource{
		"basic auth":      {Url: "http://a", Credentials: &imagev1.HttpSource_BasicAuth{BasicAuth: &imagev1.BasicAuth{}}},
		"credentials ref": {Url: "http://a", Credentials: &imagev1.HttpSource_CredentialsRef{CredentialsRef: "osm"}},
		"ca bundle":       {Url: "http://a", CaBundle: "pem"},
	}
	for name, h := range unsupported {
		t.Run(name, func(t *testing.T) {
			_, err := New().ImportSource(context.Background(), httpSource(h))
			assert.ErrorIs(t, err, apperrors.ErrUnsupported)
		})
	}
}

func TestStoredDropsInputOnlyFields(t *testing.T) {
	got := New().Stored(httpSource(&imagev1.HttpSource{Url: "http://a", ExtraHeaders: []string{"Authorization: x"}}))
	assert.Equal(t, "http://a", got.GetHttp().GetUrl())
	assert.Empty(t, got.GetHttp().GetExtraHeaders())
}
