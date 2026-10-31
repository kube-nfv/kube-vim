package cdi

import (
	"context"
	"testing"

	imagev1 "github.com/kube-nfv/kube-vim-api/pkg/apis/image/v1"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/kube-nfv/kube-vim/internal/kubevim/image"
	httpsource "github.com/kube-nfv/kube-vim/internal/kubevim/image/source/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = k8stest.TestNamespace

func newManagerWithSC(t *testing.T, storageClass string, objs ...client.Object) (*cdiManager, client.Client) {
	t.Helper()
	cl := k8stest.NewIndexedClient(t, IdIndexes, objs...)
	ns := testNamespace
	sc := storageClass
	sources := map[string]image.Source{httpsource.Kind: httpsource.New()}
	m, err := NewCDIImageManager(cl, cl, &config.ImageConfig{StorageClass: &sc}, &config.K8sConfig{Namespace: &ns}, sources)
	require.NoError(t, err)
	return m, cl
}

func newManager(t *testing.T) (*cdiManager, client.Client) {
	return newManagerWithSC(t, "fast", &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "fast"}})
}

func newImage() *imagev1.Image {
	return &imagev1.Image{
		Name:     "ubuntu",
		Metadata: map[string]string{"owner": "osm"},
		Source: &imagev1.ImageSource{Source: &imagev1.ImageSource_Http{Http: &imagev1.HttpSource{
			Url:          "http://example.com/ubuntu.qcow2",
			ExtraHeaders: []string{"Authorization: secret"},
		}}},
	}
}

// create stores an image through m and returns its id.
func create(t *testing.T, m *cdiManager) string {
	t.Helper()
	img, err := m.CreateImage(context.Background(), newImage())
	require.NoError(t, err)
	return img.ImageId
}

func TestCreateImage(t *testing.T) {
	t.Parallel()
	t.Run("creates import source and data volume", func(t *testing.T) {
		m, cl := newManager(t)
		img, err := m.CreateImage(context.Background(), newImage())
		require.NoError(t, err)
		require.NotEmpty(t, img.ImageId)
		assert.Equal(t, "ubuntu", img.Name)
		assert.Equal(t, imagev1.ImportPolicy_IMPORT_POLICY_EAGER, img.ImportPolicy)
		assert.Equal(t, imagev1.ImageStatus_IMAGE_STATUS_PENDING, img.Status)
		assert.Empty(t, img.GetSource().GetHttp().GetExtraHeaders(), "input-only headers are never returned")

		name := img.Metadata[image.PvcMetadataKey]
		vis := &v1beta1.VolumeImportSource{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, vis))
		assert.Equal(t, img.ImageId, vis.Labels[image.K8sImageIdLabel])
		assert.Equal(t, []string{"Authorization: secret"}, vis.Spec.Source.HTTP.ExtraHeaders)
		assert.NotContains(t, vis.Annotations[image.K8sImageAnnotation], "secret")
		dv := &v1beta1.DataVolume{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, dv))
		assert.Equal(t, img.ImageId, dv.Labels[image.K8sImageIdLabel])
	})

	t.Run("unsupported field is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		img := newImage()
		img.ImportPolicy = imagev1.ImportPolicy_IMPORT_POLICY_ON_FIRST_USE
		_, err := m.CreateImage(context.Background(), img)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("source without a frontend is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		img := newImage()
		img.Source = &imagev1.ImageSource{Source: &imagev1.ImageSource_S3{S3: &imagev1.S3Source{Url: "https://s3/a"}}}
		_, err := m.CreateImage(context.Background(), img)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
	})

	t.Run("missing storage class rolls back the import source", func(t *testing.T) {
		m, cl := newManagerWithSC(t, "ghost")
		_, err := m.CreateImage(context.Background(), newImage())
		require.Error(t, err)
		list := &v1beta1.VolumeImportSourceList{}
		require.NoError(t, cl.List(context.Background(), list))
		assert.Empty(t, list.Items, "import source must be cleaned up when the data volume cannot be created")
	})
}

func TestGetImage(t *testing.T) {
	t.Parallel()
	t.Run("status follows the data volume", func(t *testing.T) {
		m, cl := newManager(t)
		id := create(t, m)
		got, err := m.GetImage(context.Background(), id)
		require.NoError(t, err)
		dv := &v1beta1.DataVolume{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: got.Metadata[image.PvcMetadataKey]}, dv))
		dv.Status.Phase = v1beta1.Succeeded
		require.NoError(t, cl.Update(context.Background(), dv))

		got, err = m.GetImage(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, id, got.ImageId)
		assert.Equal(t, "ubuntu", got.Name)
		assert.Equal(t, imagev1.ImageStatus_IMAGE_STATUS_AVAILABLE, got.Status)
		assert.Equal(t, "10Gi", got.Size)
	})

	t.Run("missing image is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetImage(context.Background(), "nope")
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})
}

func TestListImages(t *testing.T) {
	t.Parallel()
	t.Run("lists images with their data volume", func(t *testing.T) {
		m, cl := newManager(t)
		ids := map[string]bool{create(t, m): true, create(t, m): true}
		orphan, err := m.GetImage(context.Background(), create(t, m))
		require.NoError(t, err)
		dv := &v1beta1.DataVolume{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: orphan.Metadata[image.PvcMetadataKey]}}
		require.NoError(t, cl.Delete(context.Background(), dv))

		got, err := m.ListImages(context.Background())
		require.NoError(t, err)
		listed := map[string]bool{}
		for _, img := range got {
			listed[img.ImageId] = true
		}
		assert.Equal(t, ids, listed)
	})

	t.Run("empty returns nothing", func(t *testing.T) {
		m, _ := newManager(t)
		got, err := m.ListImages(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestImageStatus(t *testing.T) {
	tests := map[v1beta1.DataVolumePhase]imagev1.ImageStatus{
		v1beta1.PhaseUnset:       imagev1.ImageStatus_IMAGE_STATUS_PENDING,
		v1beta1.Pending:          imagev1.ImageStatus_IMAGE_STATUS_PENDING,
		v1beta1.ImportScheduled:  imagev1.ImageStatus_IMAGE_STATUS_IMPORTING,
		v1beta1.ImportInProgress: imagev1.ImageStatus_IMAGE_STATUS_IMPORTING,
		v1beta1.UploadReady:      imagev1.ImageStatus_IMAGE_STATUS_UPLOAD_PENDING,
		v1beta1.Succeeded:        imagev1.ImageStatus_IMAGE_STATUS_AVAILABLE,
		v1beta1.Failed:           imagev1.ImageStatus_IMAGE_STATUS_FAILED,
	}
	for phase, want := range tests {
		assert.Equal(t, want, imageStatus(phase), "phase %q", phase)
	}
	assert.Equal(t, int32(45), progressPercent("45.67%"))
	assert.Equal(t, int32(0), progressPercent("N/A"))
}

func TestGetImageMissingDataVolume(t *testing.T) {
	m, cl := newManager(t)
	img, err := m.CreateImage(context.Background(), newImage())
	require.NoError(t, err)
	dv := &v1beta1.DataVolume{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: img.Metadata[image.PvcMetadataKey]}}
	require.NoError(t, cl.Delete(context.Background(), dv))

	_, err = m.GetImage(context.Background(), img.ImageId)
	assert.ErrorIs(t, err, apperrors.ErrInternal)
	assert.False(t, apierrors.IsNotFound(err))
}
