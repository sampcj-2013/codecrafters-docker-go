package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	regexp "github.com/oriser/regroup"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type (
	Manifest struct {
		Digest    string   `json:"digest"`
		MediaType string   `json:"mediaType"`
		Size      int      `json:"size"`
		Platform  Platform `json:"platform"`
	}
	Platform struct {
		Architecture string `json:"architecture"`
		Os           string `json:"os"`
	}
	Auth struct {
		Bearer  string `regroup:"bearer"`
		Service string `regroup:"service"`
		Scope   string `regroup:"scope"`
		Token   string `json:"token"`
	}
	RegistryResponse struct {
		Manifests     []Manifest `json:"manifests"`
		MediaType     string     `json:"mediaType"`
		SchemaVersion int        `json:"schemaVersion"`
	}
	OCIImageManifest struct {
		SchemaVersion uint32         `json:"schemaVersion"`
		MediaType     string         `json:"mediaType"`
		ArtifactType  string         `json:"artifactType"`
		Config        OCIImageConfig `json:"config"`
		Layers        []ImageLayer   `json:"layers"`
		Annotations   struct {
			// TODO: Support annotations according to OCI spec
		} `json:"annotations"`
	}
	DockerDistributionManifest struct {
		SchemaVersion uint32         `json:"schemaVersion"`
		MediaType     string         `json:"mediaType"`
		ArtifactType  string         `json:"artifactType"`
		Config        OCIImageConfig `json:"config"`
		Layers        []ImageLayer   `json:"layers"`
		Annotations   struct {
			// TODO: Support annotations according to Docker spec
		} `json:"annotations"`
	}
	ImageLayer struct {
		Manifest
		Sha256Sum string
		Data      bytes.Buffer
	}
	ContainerRegistryDetails struct {
		FQDN         string
		Auth         string
		Alias        string
		Scheme       string
		ManifestPath string
		TagsPath     string
		BlobsPath    string
	}
	ContainerRegistries = map[string]*ContainerRegistryDetails
	RegistrySchema      string
	OCIImageManifestV1  string
	OCIImageConfig      struct{}
	DockerImageConfig   struct{}
	// RegistryRequest contains common details for pulling image manifests and layers across various registry requests
	RegistryRequest struct {
		ImageReference string
		ImageTag       string
		Auth           *Auth
	}
	// RegistryCache comprises any cached image layers previously fetched from a registry
	// First we check the RegisryCache and then the file-system on disk for the image layer.
	// 	1. Add that to the in-memory Registry-Cache for requestAuthenticationToken
	//	2. Extract the layer to disk in the chroot/pivot_root
	// If neither the image layer exists in cache or is present on the filesystem then it
	// should be retrieved from the remote registry and the following actions should then be performed:
	//	1. Download the image layer from the remote registry
	//	2. Populate an entry in the RegistryCache
	//	3. Flush the layer to disk
	RegistryCache struct {
		Layers         map[string]*ImageLayer
		ImageReference string
		ImageTag       string
	}
)

const (
	DefaultRegistry        string             = "docker.io"
	ImageLayersPath        string             = "/tmp/containers/layers"
	OCIImageTypeManifestV1 OCIImageManifestV1 = "application/vnd.oci.image.manifest.v1+json"
	// Docker Image Manifest Version 2, Schema 2
	DockerImageTypeDistributionManifestV2     RegistrySchema = "application/vnd.docker.distribution.manifest.v2+json"
	DockerImageTypeDistributionListManifestV2 RegistrySchema = "application/vnd.docker.distribution.manifest.list.v2+json"
	DockerImageTypeContainerImageManifestV1   RegistrySchema = "application/vnd.docker.container.image.v1+json"
	DockerImageTypeRootFs                     RegistrySchema = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	DockerImageTypeRootFsForeign              RegistrySchema = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	DockerImageTypePlugin                     RegistrySchema = "application/vnd.docker.plugin.v1+json"
	OciImageIndexV1                                          = "application/vnd.oci.image.index.v1+json"
	AcceptHeaders                             string         = "application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json"
)

// RegistryCache is a map of string containing sha256:digest values pointing to ImageLayer values
var registryCache RegistryCache

// docker.io is the default registry
var Registries = ContainerRegistries{
	DefaultRegistry: &ContainerRegistryDetails{
		Alias:        DefaultRegistry,
		Auth:         "auth.docker.io",
		FQDN:         "registry-1.docker.io",
		ManifestPath: "/v2/%s/manifests/%s",
		BlobsPath:    "/v2/%s/blobs/%s",
		Scheme:       "https",
	},
}
var bearerRegex = regexp.MustCompile(`(?i)(Bearer[[:space:]]+realm="(?P<bearer>(?:\\"|.)*?)")[[:space:]]*?,[[:space:]]*?(service[[:space:]]*?="(?P<service>(?:\\"|.)*?))"[[:space:]]*?,[[:space:]]*?(scope[[:space:]]*?="(?P<scope>(?:\\"|.)*?)")`)

// auth: https://auth.docker.io/token?scope=repository:library/alpine:pull&service=registry.docker.io
// manifest:  https://registry-1.docker.io/v2/library/alpine/manifests/latest

// TODO: Implement persistent image caching and storage
// TODO: Implement image extraction
func (registry *ContainerRegistryDetails) generateManifestRequest(ref, tag string) string {
	return fmt.Sprintf("%s://%s%s", registry.Scheme, registry.FQDN, fmt.Sprintf(registry.ManifestPath, ref, tag))
}

func (registry *ContainerRegistryDetails) generateBlobRequest(ref, blob string) string {
	return fmt.Sprintf("%s://%s%s", registry.Scheme, registry.FQDN, fmt.Sprintf(registry.BlobsPath, ref, blob))
}

var defaultHTTPClient *http.Client

func init() {
	if defaultHTTPClient = createHTTPClient(); defaultHTTPClient == nil {
		fmt.Println("unable to create a default HTTP client, exiting...")
		os.Exit(1)
	}
}

// TODO: Move to net.go
func createHTTPClient() *http.Client {
	return &http.Client{
		Timeout: time.Second * 20,
		Transport: &http.Transport{
			// TLSClientConfig: &tls.Config{
			// 	InsecureSkipVerify: true,
			// },
			IdleConnTimeout: time.Second * 30,
			MaxIdleConns:    10,
			DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
			},
		},
	}
}

func pullImage(imageReference string, auth *Auth) (*[]ImageLayer, error) {
	trueImageReference, registry, tag := sanitiseImageReference(imageReference)
	registryDetails, ok := Registries[registry]
	if !ok {
		return nil, errors.New("unable to find appropriate registry for the image provided")
	}

	query := registryDetails.generateManifestRequest(trueImageReference, tag)
	req, err := http.NewRequest("GET", query, nil)
	if err != nil {
		return nil, err
	}

	if auth != nil {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", auth.Token))
	}
	req.Header.Set("Accept", AcceptHeaders)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	// Attempt to (re)authenticate
	if (resp.StatusCode > 400 && resp.StatusCode < 500) || auth == nil {
		auth, err = registryDetails.requestAuthenticationToken(resp)
		req, err := http.NewRequest("GET", query, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", auth.Token))
		req.Header.Set("Accept", AcceptHeaders)
		resp, err = defaultHTTPClient.Do(req)
	}

	if err != nil {
		return nil, err
	} else if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	body, err := io.ReadAll(resp.Body)
	contentType, ok := resp.Header["Content-Type"]
	if !ok || len(contentType) != 1 {
		return nil, errors.New("unsupported Content-Type returned from registry")
	}

	var (
		manifests RegistryResponse
		manifest  *Manifest
	)
	switch RegistrySchema(contentType[0]) {
	case DockerImageTypeDistributionListManifestV2:
		fallthrough
	case OciImageIndexV1:
		manifest, err = manifests.getDigestForSystem(body)
	default:
		return nil, errors.New("unsupported Content-Type returned from registry")
	}

	if err != nil {
		return nil, err
	}

	var layers *[]ImageLayer

	switch manifest.MediaType {
	case string(DockerImageTypeDistributionManifestV2):
		// https://registry-1.docker.io/v2/library/ubuntu/blobs/sha256:...
		query = registryDetails.generateManifestRequest(trueImageReference, manifest.Digest)
		resp, err := registryDetails.sendRequest(query, "GET", auth)
		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)

		var dockerManifest = DockerDistributionManifest{}
		err = json.Unmarshal(body, &dockerManifest)
		if err != nil {
			return nil, err
		}

		if manifest.Platform.Os != runtime.GOOS && manifest.Platform.Architecture != runtime.GOARCH {
			return nil, errors.New("no matching manifest for this system architecture found")
		}
		layers = &dockerManifest.Layers
	case string(OCIImageTypeManifestV1):
		// For this resource we need to first retrieve the image manifest hash
		// Then we can retrieve the image layer as with the returned docker image manifest
		// https://registry-1.docker.io/v2/library/ubuntu/manifests/sha256:aa772...
		// TODO: Implement handling for retrieving OCIv1 image manifests
		return nil, errors.New("not implemented")
	default:
		return nil, errors.New(fmt.Sprintf("unsupported Content-Type: %s returnend from registry", manifest.MediaType))
	}

	var registryRequest = &RegistryRequest{
		ImageReference: trueImageReference,
		ImageTag:       tag,
		Auth:           auth,
	}

	// TODO: Make this option configurable.
	var maxRetries = 5
	for retryCount := 0; retryCount < maxRetries; retryCount++ {
		err = registryDetails.fetchLayers(layers, registryRequest)
		if err != nil {
			continue
		} else {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	return layers, err
}

func (registry *ContainerRegistryDetails) sendRequest(query string, method string, auth *Auth) (*http.Response, error) {
	req, err := http.NewRequest(method, query, nil)
	if err != nil {
		return nil, err
	}

	if auth != nil {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", auth.Token))
	}

	req.Header.Set("Accept", AcceptHeaders)

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (registry RegistryCache) hasLayer(layer *ImageLayer) error {
	// In-memory cache is first checked for the layer's existence
	_, ok := registry.Layers[layer.Digest]
	// Let's try checking whether the layer on the VFS is correct,
	// meaning that its checksum matches the provided digest.
	if !ok {
		fileLayer, err := os.Open(fmt.Sprintf("%s/%s.tar.gz", ImageLayersPath, layer.Sha256Sum))
		if err != nil {
			return err
		}
		defer fileLayer.Close()

		hash := sha256.New()
		if _, err := io.Copy(hash, fileLayer); err != nil {
			return err
		}

		if string(hash.Sum(nil)) != layer.Digest {
			return errors.New("digest mismatch for existing layer and the remote")
		}
	}
	return nil
}

func (l *ImageLayer) UnmarshalJSON(data []byte) error {
	type I ImageLayer

	if err := json.Unmarshal(data, (*I)(l)); err != nil {
		return err
	}

	checksum := strings.SplitAfterN(l.Digest, "sha256:", 2)
	if len(checksum) != 2 {
		return errors.New("unexpected format for digest")
	}
	// TODO: Require more robust checking of the checksum
	l.Sha256Sum = checksum[1]
	return nil
}

// TODO: Setup a permanent image layer caching structure.
// TODO: Setup up an expiring context with retry logic to allow for some error resiliency when pulling layers concurrently
func (registry *ContainerRegistryDetails) fetchLayers(layers *[]ImageLayer, registryRequest *RegistryRequest) error {
	var (
		wg           sync.WaitGroup
		successCount atomic.Int32
	)

	for _, layer := range *layers {
		wg.Add(1)
		go func(l *ImageLayer, w *sync.WaitGroup) {
			defer w.Done()
			// Do we have the layer already in our cache?
			if err := registryCache.hasLayer(l); err == nil {
				successCount.Add(1)
				return
			}

			resp, err := registry.sendRequest(registry.generateBlobRequest(
				registryRequest.ImageReference,
				url.QueryEscape(l.Digest)),
				"GET",
				registryRequest.Auth,
			)
			if err != nil {
				return
			}

			err = copyTo(resp.Body, l)
			if err != nil {
				return
			}
			successCount.Add(1)
			return
		}(&layer, &wg)
	}
	wg.Wait()

	if int(successCount.Load()) != len(*layers) {
		return errors.New("unable to fetch all layers in image")
	}
	return nil
}

const (
	B  uint64 = 1
	KB uint64 = 1 << (10 * iota)
	MB
)

// TODO: Make this configurable
// TODO: This in-memory cache implementation is minimal and should not be used as-is.
//
//	 The in-memory cache has the following limitations:
//		1. There is no current limitation on the layer size. Resulting layers can consume more memory than
//			is available on the system.
//		2. There is no restriction on the number of layers in the cache.
//		3. The cache entries have no expiries.
const cacheEnabled = false

func copyTo(reader io.ReadCloser, l *ImageLayer) error {
	r := bufio.NewReader(reader)
	err := os.MkdirAll(ImageLayersPath, 0600)
	if err != nil {
		return errors.New("could not create directory for this image")
	}

	f, err := os.OpenFile(fmt.Sprintf("%s/%s.tar.gz", ImageLayersPath, l.Sha256Sum), os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return errors.New("could not open image file for writing")
	}

	defer f.Close()

	var writers []io.Writer
	wFile := bufio.NewWriter(f)
	writers = append(writers, wFile)
	if cacheEnabled {
		wCache := bufio.NewWriter(&l.Data)
		writers = append(writers, wCache)
	}
	defer wFile.Flush()

	mw := io.MultiWriter(writers...)
	bytesWritten, err := io.Copy(mw, r)

	if err != nil {
		return err
	}

	if bytesWritten != int64(l.Size) {
		return errors.New("written layer size does not match remote layer size")
	}

	return nil
}

func (manifests *RegistryResponse) getDigestForSystem(body []byte) (*Manifest, error) {
	err := json.Unmarshal(body, &manifests)
	if err != nil {
		return nil, err
	}

	for _, manifest := range manifests.Manifests {
		if manifest.Platform.Os == runtime.GOOS && manifest.Platform.Architecture == runtime.GOARCH {
			return &manifest, err
		}
	}
	return nil, errors.New("no digest found that supports this architecture or system")
}

func (registry *ContainerRegistryDetails) requestAuthenticationToken(response *http.Response) (*Auth, error) {
	if wwwAuth, ok := response.Header["Www-Authenticate"]; !ok {
		return nil, errors.New("no Www-Authenticate header present; cannot perform authentication")
	} else {
		auth := &Auth{}
		err := bearerRegex.MatchToTarget(wwwAuth[0], auth)
		if err != nil {
			return nil, errors.New("malformed Www-Authenticate header present; cannot perform authentication")
		}

		err = registry.constructAuth(auth)
		if err != nil {
			return nil, err
		}
		return auth, nil
	}
}

func (registry *ContainerRegistryDetails) constructAuth(auth *Auth) error {
	query := fmt.Sprintf("%s?scope=%s&service=%s", auth.Bearer, url.QueryEscape(auth.Scope), url.QueryEscape(auth.Service))
	req, err := http.NewRequest("GET", query, nil)
	if err != nil {
		return err
	}

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	err = json.Unmarshal(body, &auth)
	if err != nil {
		return err
	}
	return nil
}

func sanitiseImageReference(ref string) (string, string, string) {
	// Simplified logic for the special (registry-1)?.docker.io case
	// When providing the short form of an image reference such as "alpine" or "alpine:latest"
	// to CLI tools such as docker or podman they will "familiarise" the given image
	// reference by prepending "docker.io/library/" to it.
	var registryDomain string
	i := strings.IndexRune(ref, '/')

	if i == -1 || (!strings.ContainsAny(ref[:i], ".:")) {
		registryDomain = DefaultRegistry
	} else {
		registryDomain = ref[:i]
		ref = ref[i+1:]
	}

	var found bool
	if found = strings.HasPrefix(ref, "library/"); !found && registryDomain == DefaultRegistry {
		ref = "library/" + ref
	}

	var tag string
	// If there is no tag for the image reference use the default "latest"
	ref, tag, found = strings.Cut(ref, ":")
	if !found {
		tag = "latest"
	}
	return ref, registryDomain, tag
}
