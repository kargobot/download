package download

import (
	"fmt"
	"testing"
)

func TestDown(t *testing.T) {
	url := "https://files2.freedownloadmanager.org/6/latest/freedownloadmanager.deb"
	path := "tmp"
	var dl Downloader = New(url, path)
	// downloader := New(url, path)
	// downloader.SetDir("/tmp")
	// downloader.SetThread(2)
	if err := dl.Download(); err != nil {
		fmt.Println(err)
	}
	fp := dl.GetPath()
	md5sum, err := Md5sum(fp)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Printf("%s\t%s\n", md5sum, dl.GetFileName())
}
