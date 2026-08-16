package cdi

import (
	"context"
	"testing"

	nfvcommon "github.com/kube-nfv/kube-vim-api/pkg/apis"
	"github.com/kube-nfv/kube-vim-api/pkg/apis/admin"
	config "github.com/kube-nfv/kube-vim/internal/config/kubevim"
	apperrors "github.com/kube-nfv/kube-vim/internal/errors"
	"github.com/kube-nfv/kube-vim/internal/k8s/k8stest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testNamespace = "kube-nfv"

func newManagerWithSC(t *testing.T, storageClass string, objs ...client.Object) (*cdiManager, client.Client) {
	t.Helper()
	cl := k8stest.NewClient(t, objs...)
	ns := testNamespace
	sc := storageClass
	m, err := NewCDIImageManager(cl, cl, &config.ImageConfig{StorageClass: &sc}, &config.K8sConfig{Namespace: &ns})
	require.NoError(t, err)
	return m, cl
}

func newManager(t *testing.T, objs ...client.Object) (*cdiManager, client.Client) {
	return newManagerWithSC(t, "fast", objs...)
}

func seedVis(name string) *v1beta1.VolumeImportSource {
	meta := k8stest.ManagedMeta(name)
	meta.Namespace = testNamespace
	return &v1beta1.VolumeImportSource{
		ObjectMeta: meta,
		Spec: v1beta1.VolumeImportSourceSpec{
			Source: &v1beta1.ImportSourceType{
				HTTP: &v1beta1.DataVolumeSourceHTTP{URL: "http://example.com/" + name + ".qcow2"},
			},
		},
	}
}

func seedDv(name string) *v1beta1.DataVolume {
	meta := k8stest.ManagedMeta(name)
	meta.Namespace = testNamespace
	return &v1beta1.DataVolume{
		ObjectMeta: meta,
		Spec: v1beta1.DataVolumeSpec{
			Storage: &v1beta1.StorageSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
				},
			},
		},
		Status: v1beta1.DataVolumeStatus{Phase: v1beta1.Succeeded},
	}
}

func httpDownloadReq(name string) *admin.DownloadImageRequest {
	return &admin.DownloadImageRequest{
		Metadata: &admin.ImageMetadata{Name: name},
		Source: &admin.ImageSource{
			Type: admin.ImageSourceType_HTTP,
			Http: &admin.HttpSource{Url: "http://example.com/" + name + ".qcow2"},
		},
	}
}

func TestGetImage(t *testing.T) {
	t.Run("returns seeded image", func(t *testing.T) {
		m, _ := newManager(t, seedVis("img1"), seedDv("img1"))
		got, err := m.GetImage(context.Background(), &nfvcommon.Identifier{Value: "uid-img1"})
		require.NoError(t, err)
		assert.Equal(t, "img1", got.Name)
		assert.Equal(t, "uid-img1", got.SoftwareImageId.GetValue())
	})

	t.Run("nil id is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetImage(context.Background(), nil)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("missing image is ErrNotFound", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.GetImage(context.Background(), &nfvcommon.Identifier{Value: "uid-nope"})
		var target *apperrors.ErrNotFound
		assert.ErrorAs(t, err, &target)
	})
}

func TestListImages(t *testing.T) {
	t.Run("returns paired vis+dv images", func(t *testing.T) {
		m, _ := newManager(t, seedVis("img1"), seedDv("img1"), seedVis("img2"), seedDv("img2"))
		got, err := m.ListImages(context.Background())
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("skips a vis with no matching dv", func(t *testing.T) {
		m, _ := newManager(t, seedVis("img1"), seedDv("img1"), seedVis("orphan"))
		got, err := m.ListImages(context.Background())
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})

	t.Run("empty returns nothing", func(t *testing.T) {
		m, _ := newManager(t)
		got, err := m.ListImages(context.Background())
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestDownloadImage(t *testing.T) {
	t.Run("nil request is rejected", func(t *testing.T) {
		m, _ := newManager(t)
		_, err := m.DownloadImage(context.Background(), nil)
		var target *apperrors.ErrInvalidArgument
		assert.ErrorAs(t, err, &target)
	})

	t.Run("lazy download creates only the import source", func(t *testing.T) {
		m, cl := newManager(t)
		req := httpDownloadReq("img1")
		lazy := true
		req.Options = &admin.DownloadOptions{LazyDownload: &lazy}

		resp, err := m.DownloadImage(context.Background(), req)
		require.NoError(t, err)
		require.NotEmpty(t, resp.ImageId.GetValue())

		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "img1"}, &v1beta1.VolumeImportSource{}))
		err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "img1"}, &v1beta1.DataVolume{})
		assert.True(t, apierrors.IsNotFound(err), "no DataVolume for lazy download")
	})

	t.Run("full download creates import source and data volume", func(t *testing.T) {
		m, cl := newManager(t, &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "fast"}})
		resp, err := m.DownloadImage(context.Background(), httpDownloadReq("img1"))
		require.NoError(t, err)
		require.NotEmpty(t, resp.ImageId.GetValue())

		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "img1"}, &v1beta1.DataVolume{}))
		vis := &v1beta1.VolumeImportSource{}
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "img1"}, vis))
		assert.NotEmpty(t, vis.Labels[K8sDataVolumeIdLabel], "vis should be labelled with the data volume id")
	})

	t.Run("unsupported source type is rejected and rolls back", func(t *testing.T) {
		m, cl := newManager(t)
		req := httpDownloadReq("img1")
		req.Source.Type = admin.ImageSourceType(99)
		req.Source.Http = nil
		_, err := m.DownloadImage(context.Background(), req)
		assert.ErrorIs(t, err, apperrors.ErrUnsupported)
		// Nothing should be created when the source cannot be converted.
		err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "img1"}, &v1beta1.VolumeImportSource{})
		assert.True(t, apierrors.IsNotFound(err))
	})

	t.Run("missing storage class rolls back the import source", func(t *testing.T) {
		m, cl := newManagerWithSC(t, "ghost")
		_, err := m.DownloadImage(context.Background(), httpDownloadReq("img1"))
		require.Error(t, err)
		err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "img1"}, &v1beta1.VolumeImportSource{})
		assert.True(t, apierrors.IsNotFound(err), "import source must be cleaned up when the data volume cannot be created")
	})
}
