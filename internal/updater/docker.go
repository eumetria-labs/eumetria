package updater

// A small client for the Docker Engine API over its unix socket: only the
// calls the updater needs, without the Docker SDK and its dependencies.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiVersion is the oldest Engine API the updater speaks: Docker 25.
const apiVersion = "v1.44"

// Container is what the updater reads of a container.
type Container struct {
	ID     string
	Name   string
	Image  string // the image ID it runs
	Ref    string // the image it was made from (Config.Image)
	Labels map[string]string
	State  string // running, exited, …
	Health string // healthy, unhealthy, starting, or "" without a check
}

// Image is what the updater reads of an image.
type Image struct {
	ID          string
	RepoDigests []string
	Labels      map[string]string
}

// Docker is the Engine API as the updater uses it; tests fake it.
type Docker interface {
	Container(ctx context.Context, id string) (Container, error)
	// Project lists the containers of a compose project, stopped ones too.
	Project(ctx context.Context, project string) ([]Container, error)
	Image(ctx context.Context, ref string) (Image, error)
	Tag(ctx context.Context, image, repo, tag string) error
	// RemoteDigest asks the registry (through the daemon) for ref's digest.
	RemoteDigest(ctx context.Context, ref string) (string, error)
	HostName(ctx context.Context) (string, error)
	Logs(ctx context.Context, id string, lines int) (string, error)
	// Handover starts a short-lived container of image with the volumes of
	// from, running args; it removes itself when done.
	Handover(ctx context.Context, image, from string, args []string) error
}

// Client talks to the daemon over socket.
type Client struct {
	http *http.Client
}

// NewClient is a client for the daemon at socket (/var/run/docker.sock).
func NewClient(socket string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: tr, Timeout: 2 * time.Minute}}
}

// apiError is an answer other than 2xx, with the daemon's message.
type apiError struct {
	Status  int
	Message string
}

func (e apiError) Error() string { return fmt.Sprintf("docker: %d %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	u := "http://docker/" + apiVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		var m struct{ Message string }
		json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&m)
		return apiError{res.StatusCode, m.Message}
	}
	if out == nil {
		io.Copy(io.Discard, res.Body)
		return nil
	}
	if w, ok := out.(*bytes.Buffer); ok {
		_, err = io.Copy(w, io.LimitReader(res.Body, 1<<20))
		return err
	}
	return json.NewDecoder(res.Body).Decode(out)
}

type inspected struct {
	ID     string `json:"Id"`
	Name   string
	Image  string
	Config struct {
		Image  string
		Labels map[string]string
	}
	State struct {
		Status string
		Health *struct{ Status string }
	}
}

func (x inspected) container() Container {
	c := Container{ID: x.ID, Name: strings.TrimPrefix(x.Name, "/"), Image: x.Image, Ref: x.Config.Image, Labels: x.Config.Labels, State: x.State.Status}
	if x.State.Health != nil {
		c.Health = x.State.Health.Status
	}
	return c
}

func (c *Client) Container(ctx context.Context, id string) (Container, error) {
	var x inspected
	err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil, &x)
	return x.container(), err
}

func (c *Client) Project(ctx context.Context, project string) ([]Container, error) {
	f, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + project}})
	var list []struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"1"}, "filters": {string(f)}}, nil, &list); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(list))
	for _, l := range list {
		x, err := c.Container(ctx, l.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// Image refs keep their slashes in the path: the daemon reads them so.
func refPath(ref string) string { return strings.ReplaceAll(url.PathEscape(ref), "%2F", "/") }

func (c *Client) Image(ctx context.Context, ref string) (Image, error) {
	var x struct {
		ID          string `json:"Id"`
		RepoDigests []string
		Config      struct{ Labels map[string]string }
	}
	err := c.do(ctx, http.MethodGet, "/images/"+refPath(ref)+"/json", nil, nil, &x)
	return Image{ID: x.ID, RepoDigests: x.RepoDigests, Labels: x.Config.Labels}, err
}

func (c *Client) Tag(ctx context.Context, image, repo, tag string) error {
	return c.do(ctx, http.MethodPost, "/images/"+refPath(image)+"/tag", url.Values{"repo": {repo}, "tag": {tag}}, nil, nil)
}

func (c *Client) RemoteDigest(ctx context.Context, ref string) (string, error) {
	var x struct{ Descriptor struct{ Digest string } }
	err := c.do(ctx, http.MethodGet, "/distribution/"+refPath(ref)+"/json", nil, nil, &x)
	return x.Descriptor.Digest, err
}

func (c *Client) HostName(ctx context.Context) (string, error) {
	var x struct{ Name string }
	err := c.do(ctx, http.MethodGet, "/info", nil, nil, &x)
	return x.Name, err
}

func (c *Client) Logs(ctx context.Context, id string, lines int) (string, error) {
	var b bytes.Buffer
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {fmt.Sprint(lines)}}
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q, nil, &b); err != nil {
		return "", err
	}
	return demux(b.Bytes()), nil
}

// demux takes the stream headers out of a log without a TTY: each frame is
// 8 bytes (stream, 0, 0, 0, size) and size bytes of text.
func demux(raw []byte) string {
	var out strings.Builder
	for len(raw) >= 8 {
		n := int(binary.BigEndian.Uint32(raw[4:8]))
		if raw[0] > 2 || raw[1]|raw[2]|raw[3] != 0 || 8+n > len(raw) {
			return string(raw) // not framed: a TTY's plain text
		}
		out.Write(raw[8 : 8+n])
		raw = raw[8+n:]
	}
	return out.String()
}

func (c *Client) Handover(ctx context.Context, image, from string, args []string) error {
	cfg := map[string]any{
		"Image":  image,
		"Cmd":    args,
		"Labels": map[string]string{"eumetria.updater": "handover"},
		"HostConfig": map[string]any{
			"VolumesFrom": []string{from},
			"AutoRemove":  true,
			"NetworkMode": "none",
		},
	}
	// One left from a run that didn't end goes first.
	c.do(ctx, http.MethodDelete, "/containers/eumetria-updater-handover", url.Values{"force": {"1"}}, nil, nil)
	var made struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/create", url.Values{"name": {"eumetria-updater-handover"}}, cfg, &made); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/containers/"+made.ID+"/start", nil, nil, nil)
}
