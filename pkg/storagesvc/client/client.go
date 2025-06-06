/*
Copyright 2016 The Fission Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"

	"errors"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/context/ctxhttp"

	"github.com/fission/fission/pkg/storagesvc"
)

type (
	ClientInterface interface {
		Upload(ctx context.Context, filePath string, metadata *map[string]string) (string, error)
		GetUrl(id string) string
		List(ctx context.Context) ([]string, error)
		Download(ctx context.Context, id string, filePath string) error
		GetFile(ctx context.Context, id string) (*http.Response, error)
		Delete(ctx context.Context, id string) error
	}
	client struct {
		url        string
		httpClient *http.Client
	}
)

// Client creates a storage service client.
func MakeClient(url string) ClientInterface {
	hc := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	return &client{
		url:        strings.TrimSuffix(url, "/") + "/v1",
		httpClient: hc,
	}
}

// Upload sends the local file pointed to by filePath to the storage
// service, along with the metadata.  It returns a file ID that can be
// used to retrieve the file.
func (c *client) Upload(ctx context.Context, filePath string, metadata *map[string]string) (string, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	fileSize := fi.Size()

	buf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(buf)
	fileWriter, err := bodyWriter.CreateFormFile("uploadfile", filePath)
	if err != nil {
		return "", err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}

	_, err = io.Copy(fileWriter, f)
	if err != nil {
		return "", err
	}

	contentType := bodyWriter.FormDataContentType()
	bodyWriter.Close()

	req, err := http.NewRequest(http.MethodPost, c.url+"/archive", buf)
	if err != nil {
		return "", err
	}
	req.Header["X-File-Size"] = []string{fmt.Sprintf("%v", fileSize)}
	req.Header["Content-Type"] = []string{contentType}

	resp, err := ctxhttp.Do(ctx, c.httpClient, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("Upload error %v", resp.Status)
		return "", errors.New(msg)
	}

	var ur storagesvc.UploadResponse
	err = json.Unmarshal(body, &ur)
	if err != nil {
		return "", err
	}

	return ur.ID, nil
}

// GetUrl returns an HTTP URL that can be used to download the file pointed to by ID
func (c *client) GetUrl(id string) string {
	return fmt.Sprintf("%v/archive?id=%v", c.url, url.PathEscape(id))
}

func (c *client) List(ctx context.Context) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, c.url+"/archive", nil)
	if err != nil {
		return []string{}, err
	}
	resp, err := ctxhttp.Do(ctx, c.httpClient, req)
	if err != nil {
		return []string{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []string{}, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("List error %v", resp.Status)
		return []string{}, errors.New(msg)
	}

	var ids []string
	err = json.Unmarshal(body, &ids)
	if err != nil {
		return []string{}, err
	}
	return ids, nil
}

// Download fetches the file identified by ID to the local file path.
// filePath must not exist.
func (c *client) Download(ctx context.Context, id string, filePath string) error {
	// url for id
	url := c.GetUrl(id)

	// quit if file exists
	_, err := os.Stat(filePath)
	if err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("file already exists: %v", filePath)
	}

	// create
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	// make request
	resp, err := ctxhttp.Get(ctx, c.httpClient, url)
	if err != nil {
		fmt.Println(err)
		os.Remove(filePath)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP error %v", resp.StatusCode)
		os.Remove(filePath)
		return errors.New(msg)
	}

	// download and write data
	_, err = io.Copy(f, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

// Download fetches the file identified by ID to the local file path.
// filePath must not exist.
func (c *client) GetFile(ctx context.Context, id string) (resp *http.Response, err error) {
	// url for id
	url := c.GetUrl(id)

	// make request
	resp, err = ctxhttp.Get(ctx, c.httpClient, url)
	if err != nil {
		return resp, err
	}

	return resp, err
}

func (c *client) Delete(ctx context.Context, id string) error {
	url := c.GetUrl(id)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := ctxhttp.Do(ctx, c.httpClient, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error %v", resp.StatusCode)
	}

	return nil
}
