package main

import (
	"os"
	"net"
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"io/ioutil"
	regexp "github.com/oriser/regroup"

)

type OCIImageConfig struct{}

type OCIImageManifest struct {
	schemaManifest uint32         `json`
	mediaType      string         `json`
	artifactType   string         `json`
	config         OCIImageConfig `json`
	layers         []ImageLayer   `json`
	annotations    struct {
	// TODO: Support annotations according to OCI spec
	}
}

// OCI Image Manifest Version 1
type OCIImageManifestV1 string

const (
	OCIImageTypeManifestV1 OCIImageManifestV1 = "application/vnd.oci.image.manifest.v1+json"
)

// Docker Image Manifest Version 2, Schema 2
type DockerImageManifestVersion2Schema2 string

const (
	DockerImageTypeDistributionManifestV2     DockerImageManifestVersion2Schema2 = "application/vnd.docker.distribution.manifest.v2+json"
	DockerImageTypeDistributionListManifestV2 DockerImageManifestVersion2Schema2 = "application/vnd.docker.distribution.manifest.list.v2+json"
	DockerImageTypeContainerImageManifestV1   DockerImageManifestVersion2Schema2 = "application/vnd.docker.container.image.v1+json"
	DockerImageTypeRootFs                     DockerImageManifestVersion2Schema2 = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	DockerImageTypeRootFsForeign              DockerImageManifestVersion2Schema2 = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	DockerImageTypePlugin                     DockerImageManifestVersion2Schema2 = "application/vnd.docker.plugin.v1+json"
)

type ImageLayer struct {
	CheckSum string
	Content  []byte
}

type ImagePullRequest struct {
	ImageName   string
	ImageLayers []ImageLayer
}

type ContainerRegistryDetails struct {
	FQDN         string
	Auth         string
	Alias        string
	Scheme       string
	ManifestPath string
	TagsPath     string
	BlobsPath    string
}

type ContainerRegistries = map[string]*ContainerRegistryDetails

// docker.io is the default registry
var Registries = ContainerRegistries{
	"docker.io": &ContainerRegistryDetails{
		Alias:        "docker.io",
		Auth:         "auth.docker.io",
		FQDN:         "registry-1.docker.io",
		ManifestPath: "/v2/%s/manifests/%s",
		BlobsPath:    "/v2/%s/blobs/%s",
		Scheme:       "https",
	},
}

// auth: https://auth.docker.io/token?scope=repository:library/alpine:pull&service=registry.docker.io
// manifest:  https://registry-1.docker.io/v2/library/alpine/manifests/latest

// TODO: Implement persistent image caching and storage
// TODO: Implement image extraction

func (registry *ContainerRegistryDetails) createImageRequest(ref, tag string) string {
	return fmt.Sprintf("%s://%s%s", registry.Scheme, registry.FQDN, fmt.Sprintf(registry.ManifestPath, ref, tag))
}

var defaultHTTPClient *http.Client

func init() {
	if defaultHTTPClient = createHTTPClient(); defaultHTTPClient == nil {
		fmt.Println("Unable to create a default HTTP client, exiting...")
		os.Exit(1)
	}
}

// TODO: Move to net.go
func createHTTPClient() *http.Client {
	return &http.Client{
		Timeout: time.Second * 10,
		Transport: &http.Transport{
			// TLSClientConfig: &tls.Config{
			// 	InsecureSkipVerify: true,
			// },
			IdleConnTimeout: time.Second * 30,
			MaxIdleConns: 10,
			DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
			},
		},
	}
}

func pullImage(imageReference string, auth *Auth) error {
	trueImageReference, registry, tag := sanitiseImageReference(imageReference)
	registryDetails, ok := Registries[registry]
	if !ok {
		return errors.New("Unable to find appropriate registry for the image provided")
	}
	query := registryDetails.createImageRequest(trueImageReference, tag)

	//https://registry-1.docker.io/v2/library/alpine/manifests/latest
	resp, err := defaultHTTPClient.Get(query)
	if err != nil {
		return err
	}
	defer resp.Body.Close()


	// Attempt to (re)authenticate
	if (resp.StatusCode > 400 && resp.StatusCode < 500) || auth == nil {
		auth, err = registryDetails.requestAuthenticationToken(resp)
		req, err := http.NewRequest("GET", query, nil)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", auth.Token))
		resp, err = defaultHTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
	}

	if err != nil {
		return err
	}

	_, err = ioutil.ReadAll(resp.Body)
	return err
}

type Auth struct {
	Bearer  string `regroup:"bearer"`
	Service string `regroup:"service"`
	Scope	string `regroup:"scope"`
	Token   string `json:"token"`
}

var bearerRegex = regexp.MustCompile(`(?i)(Bearer[[:space:]]+realm="(?P<bearer>(?:\\"|.)*?)")[[:space:]]*?,[[:space:]]*?(service[[:space:]]*?="(?P<service>(?:\\"|.)*?))"[[:space:]]*?,[[:space:]]*?(scope[[:space:]]*?="(?P<scope>(?:\\"|.)*?)")`)

func (registry *ContainerRegistryDetails) requestAuthenticationToken(response *http.Response) (*Auth, error) {
	if wwwAuth, ok := response.Header["Www-Authenticate"]; !ok {
		return nil, errors.New("No Www-Authenticate header present; cannot perform authentication")
	} else {
		auth := &Auth{}
		err := bearerRegex.MatchToTarget(wwwAuth[0], auth)
		if err != nil {
			return nil, errors.New("Malformed Www-Authenticate header present; cannot perform authentication")
		}
		// https://auth.docker.io/token?scope=repository%3Alibrary%2Falpine%3Apull&service=registry.docker.io

		err = registry.ConstructAuth(auth)
		if err != nil {
			return nil, err
		}
		return auth, nil
	}
}

func (registry *ContainerRegistryDetails) ConstructAuth(auth *Auth) error {
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

	body, err := ioutil.ReadAll(resp.Body)

	err = json.Unmarshal(body, &auth)
	if err != nil {
		return err
	}
	return nil
}

const DefaultRegistry string = "docker.io"

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

func fetchLayer() {
}

func extractLayer() {
}

func extractLayersToChroot() {
}
