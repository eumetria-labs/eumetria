package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Backups go to Google Drive with plain REST calls and the token the Health
// data uses. The drive.file scope lets the app see and delete only what it
// created there, nothing else in the Drive.

const DriveScope = "https://www.googleapis.com/auth/drive.file"

const folderType = "application/vnd.google-apps.folder"

// DriveFile is a file the app made.
type DriveFile struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"createdTime"`
}

// Drive calls the Drive API on behalf of the token's user.
type Drive struct {
	c           *Client
	HTTP        *http.Client
	API, Upload string
}

// Drive returns the Drive API for this client's token.
func (c *Client) Drive() *Drive {
	// An archive takes minutes on a slow uplink, so no overall timeout: the
	// caller's context bounds it.
	return &Drive{c: c, HTTP: &http.Client{}, API: "https://www.googleapis.com/drive/v3", Upload: "https://www.googleapis.com/upload/drive/v3"}
}

func (d *Drive) request(ctx context.Context, method, u, ctype string, body io.Reader, size int64) (*http.Response, error) {
	tok, err := d.c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if size > 0 {
		req.ContentLength = size
	}
	res, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("google drive: HTTP %d: is Drive allowed? Use Connect again in the settings: %s", res.StatusCode, msg)
		}
		return nil, fmt.Errorf("google drive: HTTP %d: %s", res.StatusCode, msg)
	}
	return res, nil
}

func (d *Drive) json(ctx context.Context, method, u string, in, out any) error {
	var body io.Reader
	ctype := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ctype = strings.NewReader(string(b)), "application/json"
	}
	res, err := d.request(ctx, method, u, ctype, body, 0)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func (d *Drive) list(ctx context.Context, q string) ([]DriveFile, error) {
	var out []DriveFile
	token := ""
	for {
		v := url.Values{"q": {q}, "fields": {"nextPageToken,files(id,name,createdTime)"}, "pageSize": {"200"}, "spaces": {"drive"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page struct {
			NextPageToken string      `json:"nextPageToken"`
			Files         []DriveFile `json:"files"`
		}
		if err := d.json(ctx, http.MethodGet, d.API+"/files?"+v.Encode(), nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Files...)
		if token = page.NextPageToken; token == "" {
			return out, nil
		}
	}
}

// Folder finds the folder of that name the app made, or makes it.
func (d *Drive) Folder(ctx context.Context, name string) (string, error) {
	found, err := d.list(ctx, "name = "+quote(name)+" and mimeType = "+quote(folderType)+" and trashed = false")
	if err != nil {
		return "", err
	}
	if len(found) > 0 {
		return found[0].ID, nil
	}
	var made DriveFile
	err = d.json(ctx, http.MethodPost, d.API+"/files?fields=id", map[string]any{"name": name, "mimeType": folderType}, &made)
	return made.ID, err
}

// Files lists what the folder holds.
func (d *Drive) Files(ctx context.Context, folder string) ([]DriveFile, error) {
	return d.list(ctx, quote(folder)+" in parents and trashed = false")
}

// Put uploads data into the folder as one resumable session: the file is
// streamed, not read into memory.
func (d *Drive) Put(ctx context.Context, folder, name string, data io.Reader, size int64) (string, error) {
	meta, err := json.Marshal(map[string]any{"name": name, "parents": []string{folder}})
	if err != nil {
		return "", err
	}
	res, err := d.request(ctx, http.MethodPost, d.Upload+"/files?uploadType=resumable&fields=id", "application/json; charset=UTF-8", strings.NewReader(string(meta)), 0)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	session := res.Header.Get("Location")
	if session == "" {
		return "", errors.New("google drive: no upload session")
	}
	res, err = d.request(ctx, http.MethodPut, session, "application/octet-stream", data, size)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var made DriveFile
	if err := json.NewDecoder(res.Body).Decode(&made); err != nil {
		return "", err
	}
	return made.ID, nil
}

// PutFile uploads a file from disk.
func (d *Drive) PutFile(ctx context.Context, folder, path, name string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	return d.Put(ctx, folder, name, f, info.Size())
}

// Delete removes a file for good.
func (d *Drive) Delete(ctx context.Context, id string) error {
	return d.json(ctx, http.MethodDelete, d.API+"/files/"+url.PathEscape(id), nil, nil)
}

// Test uploads a small file to the folder and deletes it again.
func (d *Drive) Test(ctx context.Context, folder string) error {
	const text = "Eumetria checks that it can write here. This file should be gone already.\n"
	id, err := d.Put(ctx, folder, "eumetria-test-"+strconv.FormatInt(time.Now().Unix(), 10)+".txt", strings.NewReader(text), int64(len(text)))
	if err != nil {
		return err
	}
	return d.Delete(ctx, id)
}
