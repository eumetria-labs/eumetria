package main

// The updater container (PLAN 15b, internal/updater), as Eumetria reaches
// it: over the internal network, with the token the updater wrote into the
// volume Eumetria mounts read-only. Without that token there is no updater
// (a plain docker run, or compose.yaml without it), and Settings → Update
// shows the command to run instead of a button.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"eumetria/internal/updater"
)

// updaterClient talks to the updater; nil when there is none.
type updaterClient struct {
	url, token string
	http       *http.Client
}

// errNoUpdater: no token, so no updater next to this Eumetria.
var errNoUpdater = errors.New("no updater")

// newUpdaterClient reads the token each time: the updater writes it on its
// first start, which may come after Eumetria's.
func newUpdaterClient() (*updaterClient, error) {
	file := env("UPDATER_TOKEN_FILE", "/updater/"+updater.TokenFile)
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, errNoUpdater
	}
	tok := strings.TrimSpace(string(b))
	if len(tok) < 32 {
		return nil, errNoUpdater
	}
	return &updaterClient{url: strings.TrimRight(env("UPDATER_URL", "http://updater:8080"), "/"), token: tok,
		http: &http.Client{Timeout: 20 * time.Second}}, nil
}

func (c *updaterClient) call(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		var e struct{ Error string }
		json.Unmarshal(raw, &e)
		return res.StatusCode, fmt.Errorf("updater: %d %s", res.StatusCode, e.Error)
	}
	if out != nil {
		return res.StatusCode, json.Unmarshal(raw, out)
	}
	return res.StatusCode, nil
}

// Status is the updater's view: tag, host, services, job, history.
func (c *updaterClient) Status(ctx context.Context) (updater.Status, error) {
	var st updater.Status
	_, err := c.call(ctx, http.MethodGet, "/status", nil, &st)
	return st, err
}

// updaterView is the updater as Settings → Update shows it.
type updaterView struct {
	Present bool            `json:"present"`
	Error   string          `json:"error,omitempty"`
	Status  *updater.Status `json:"status,omitempty"`
}

// updaterInfo asks the updater for its status, briefly: the page doesn't
// wait long for an updater that is restarting.
func updaterInfo(ctx context.Context) updaterView {
	c, err := newUpdaterClient()
	if err != nil {
		return updaterView{}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	st, err := c.Status(ctx)
	if err != nil {
		return updaterView{Present: true, Error: err.Error()}
	}
	return updaterView{Present: true, Status: &st}
}

// Check asks the updater whether the registry has newer images under the
// current tag (a security rebuild, or a new version).
func (c *updaterClient) Check(ctx context.Context) ([]updater.Newer, error) {
	var out struct {
		Images []updater.Newer `json:"images"`
	}
	_, err := c.call(ctx, http.MethodPost, "/check", struct{}{}, &out)
	return out.Images, err
}

// Start asks for an update to tag; backup names the backup made before.
func (c *updaterClient) Start(ctx context.Context, tag, backup string) (updater.Job, error) {
	var j updater.Job
	_, err := c.call(ctx, http.MethodPost, "/update", map[string]string{"tag": tag, "backup": backup}, &j)
	return j, err
}
