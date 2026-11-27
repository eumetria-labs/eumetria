// eumetria-updater is the container that updates Eumetria in place (PLAN
// phase 15b; see internal/updater). It runs next to Eumetria in the same
// compose project, with the Docker socket and the project's folder mounted
// at the same path as on the host, and answers Eumetria only.
//
//	eumetria-updater                 serves the API on :8080
//	eumetria-updater -handover …     the helper that replaces the updater itself
//	eumetria-updater -healthcheck URL
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"eumetria/internal/updater"
)

func main() {
	var (
		listen   = flag.String("listen", ":8080", "address of the API (on the internal network only)")
		state    = flag.String("state", "/state", "folder for the token and the history (a volume Eumetria mounts read-only)")
		socket   = flag.String("socket", "/var/run/docker.sock", "the Docker socket")
		compose  = flag.String("compose", "/usr/local/bin/docker-compose", "the docker compose binary")
		prefix   = flag.String("prefix", env("UPDATER_IMAGE_PREFIX", "ghcr.io/eumetria-labs/eumetria"), "Eumetria's image; -claude-runner and -updater are added for the others")
		health   = flag.String("healthcheck", "", "GET this URL, exit 0 if it answers 200, and exit")
		handover = flag.Bool("handover", false, "replace the updater container (run by the updater itself) and exit")
		project  = flag.String("project", "", "with -handover: the compose project")
		dir      = flag.String("dir", "", "with -handover: its folder")
		service  = flag.String("service", "updater", "with -handover: the updater's service")
		files    = flag.String("files", "", "with -handover: the compose files, comma-separated")
	)
	flag.Parse()
	log.SetFlags(log.Ltime)

	if *health != "" {
		res, err := (&http.Client{Timeout: 5 * time.Second}).Get(*health)
		if err != nil || res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if *handover {
		// Give the old updater a moment to answer the last status, then
		// recreate it from the new image.
		time.Sleep(3 * time.Second)
		run := composer(*compose, *socket, *project, *dir, split(*files))
		out, err := run(context.Background(), "up", "--detach", "--no-deps", *service)
		if err != nil {
			log.Fatalf("handover: %v: %s", err, out)
		}
		log.Printf("handover: the updater runs the new image")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	docker := updater.NewClient(*socket)
	host, _ := os.Hostname()
	self, err := docker.Container(ctx, host)
	if err != nil {
		log.Fatalf("finding my own container (%s) through %s: %v", host, *socket, err)
	}
	proj, wd := self.Labels["com.docker.compose.project"], self.Labels["com.docker.compose.project.working_dir"]
	cfgFiles := split(self.Labels["com.docker.compose.project.config_files"])
	if proj == "" || wd == "" {
		log.Fatalf("not started by docker compose: the updater only works in a compose project")
	}
	for _, f := range cfgFiles {
		if _, err := os.Stat(f); err != nil {
			// Not mounted, or not root's: without capabilities, root here
			// reads and writes only what root owns (the folder and .env).
			log.Fatalf("%v: mount the project's folder at the same path as on the host (- %s:%s), owned by root", err, wd, wd)
		}
	}
	tok, err := updater.Token(*state)
	if err != nil {
		log.Fatalf("token: %v", err)
	}
	u := &updater.Updater{
		Docker:  docker,
		Compose: composer(*compose, *socket, proj, wd, cfgFiles),
		Project: proj, Dir: wd, Files: cfgFiles, Prefix: *prefix, Self: self.ID, State: *state,
	}
	srv := &http.Server{Addr: *listen, Handler: u.Handler(tok), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Printf("eumetria-updater for project %s in %s, tag %s, on %s", proj, wd, u.Tag(), *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// composer runs docker compose for the project, as it would run on the
// host: same folder, same files, the .env there.
func composer(bin, socket, project, dir string, files []string) updater.Compose {
	return func(ctx context.Context, args ...string) (string, error) {
		all := []string{"--project-name", project, "--project-directory", dir}
		for _, f := range files {
			all = append(all, "--file", f)
		}
		cmd := exec.CommandContext(ctx, bin, append(all, args...)...)
		cmd.Dir = dir
		cmd.Env = []string{"DOCKER_HOST=unix://" + socket, "HOME=/tmp", "PATH=/usr/local/bin:/usr/bin:/bin"}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		if err != nil {
			err = fmt.Errorf("%s %s: %w", filepath.Base(bin), strings.Join(args, " "), err)
		}
		return out.String(), err
	}
}

func split(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
