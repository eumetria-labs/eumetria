package main

import (
	"path/filepath"
	"testing"
)

func TestFilesDefaultToTheDataFolder(t *testing.T) {
	c := config{dataDir: "/data"}
	c.inData()
	want := config{
		dataDir: "/data", dbPath: "/data/app.db", historyPath: "/data/history.json",
		photoDir: "/data/photos", googleToken: "/data/google-token.json",
	}
	if c != want {
		t.Errorf("got %+v\nwant %+v", c, want)
	}

	// A file given by its own flag stays where it was put (the demo writes
	// site/stats.json for the smoke test); the database follows the folder.
	c = config{dataDir: "data", outPath: "site/stats.json"}
	c.inData()
	if c.outPath != "site/stats.json" || c.historyPath != filepath.Join("data", "history.json") || c.dbPath != filepath.Join("data", "app.db") {
		t.Errorf("got %+v", c)
	}
}
