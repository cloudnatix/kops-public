/*
Copyright 2020 The Kubernetes Authors.

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

package vfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/Azure/azure-storage-blob-go/azblob"
)

const (
	azureBlobSchema = "azureblob"
)

// AzureBlobPath is a path in the VFS space backed by Azure Blob.
type AzureBlobPath struct {
	client    *azureClient
	container string
	key       string
}

var _ Path = &AzureBlobPath{}
var _ HasHash = &S3Path{}

// NewAzureBlobPath returns a new AzureBlobPath.
func NewAzureBlobPath(client *azureClient, container string, key string) *AzureBlobPath {
	return &AzureBlobPath{
		client:    client,
		container: strings.TrimSuffix(container, "/"),
		key:       strings.TrimPrefix(key, "/"),
	}
}

// Base returns the base name (last element).
func (p *AzureBlobPath) Base() string {
	return path.Base(p.key)
}

// Path returns a string representing the full path.
func (p *AzureBlobPath) Path() string {
	return fmt.Sprintf("%s://%s/%s", azureBlobSchema, p.container, p.key)
}

// Join returns a new path that joins the current path and given relative paths.
func (p *AzureBlobPath) Join(relativePath ...string) Path {
	args := []string{p.key}
	args = append(args, relativePath...)
	joined := path.Join(args...)
	return &AzureBlobPath{
		client:    p.client,
		container: p.container,
		key:       joined,
	}
}

// ReadFile returns the content of the blob.
func (p *AzureBlobPath) ReadFile() ([]byte, error) {
	var b bytes.Buffer
	_, err := p.WriteTo(&b)
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// WriteTo writes the content of the blob to the writer.
func (p *AzureBlobPath) WriteTo(w io.Writer) (n int64, err error) {
	cURL, err := p.client.newContainerURL(p.container)
	if err != nil {
		return 0, err
	}
	resp, err := cURL.NewBlockBlobURL(p.key).Download(
		context.TODO(),
		0, /* offset */
		azblob.CountToEnd,
		azblob.BlobAccessConditions{},
		false /* rangeGetContentMD5 */)
	if err != nil {
		serr, ok := err.(azblob.StorageError)
		if ok && serr.ServiceCode() == azblob.ServiceCodeBlobNotFound {
			return 0, os.ErrNotExist
		}
		return 0, err
	}
	return io.Copy(w, resp.Body(azblob.RetryReaderOptions{MaxRetryRequests: 10}))
}

// CreateFile writes the file contents only if the file does not already exist.
//
// TODO(kenji): Handle a case where CreateFile is called concurrently to the same blob.
func (p *AzureBlobPath) CreateFile(data io.ReadSeeker, acl ACL) error {
	// Check if the blob exists.
	_, err := p.ReadFile()
	if err == nil {
		return os.ErrExist
	}
	if !os.IsNotExist(err) {
		return err
	}
	return p.WriteFile(data, acl)
}

// WriteFile writes the blob to the reader.
//
// TODO(kenji): Support ACL.
func (p *AzureBlobPath) WriteFile(data io.ReadSeeker, acl ACL) error {
	cURL, err := p.client.newContainerURL(p.container)
	if err != nil {
		return err
	}
	// Use block blob. Other options are page blobs (optimized for
	// random read/write) and append blob (optimized for append).
	_, err = cURL.NewBlockBlobURL(p.key).Upload(
		context.TODO(),
		data,
		azblob.BlobHTTPHeaders{ContentType: "text/plain"},
		azblob.Metadata{},
		azblob.BlobAccessConditions{},
	)
	return err
}

// Remove deletes the blob.
func (p *AzureBlobPath) Remove() error {
	cURL, err := p.client.newContainerURL(p.container)
	if err != nil {
		return err
	}
	// Delete the blob, but keep its snapshot.
	_, err = cURL.NewBlockBlobURL(p.key).Delete(context.TODO(), azblob.DeleteSnapshotsOptionNone, azblob.BlobAccessConditions{})
	return err
}

// ReadDir lists the blobs under the current Path.
func (p *AzureBlobPath) ReadDir() ([]Path, error) {
	var paths []Path
	cURL, err := p.client.newContainerURL(p.container)
	if err != nil {
		return nil, err
	}

	prefix := p.key
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	ctx := context.TODO()
	for m := (azblob.Marker{}); m.NotDone(); {
		// List all blobs that have the same prefix (without
		// recursion).  By specifying "/", the request will
		// group blobs with their names up to the appearance of "/".
		//
		// Suppose that we have the following blobs:
		//
		// - cluster/cluster.spec
		// - cluster/config
		// - cluster/instancegroup/master-eastus-1
		//
		// When the prefix is set to "cluster/", the request
		// returns "cluster/cluster.spec" and "cluster/config" in BlobItems
		// and returns "cluster/instancegroup/" in BlobPrefixes.
		resp, err := cURL.ListBlobsHierarchySegment(
			ctx,
			m,
			"/", /* delimiter */
			azblob.ListBlobsSegmentOptions{Prefix: prefix})
		if err != nil {
			return nil, nil
		}
		for _, item := range resp.Segment.BlobItems {
			paths = append(paths, &AzureBlobPath{
				client:    p.client,
				container: p.container,
				key:       item.Name,
			})
		}
		for _, prefix := range resp.Segment.BlobPrefixes {
			paths = append(paths, &AzureBlobPath{
				client:    p.client,
				container: p.container,
				key:       prefix.Name,
			})
		}

		m = resp.NextMarker

	}
	return paths, nil
}

// ReadTree lists all blobs (recursively) in the subtree rooted at the current Path.
func (p *AzureBlobPath) ReadTree() ([]Path, error) {
	var paths []Path
	cURL, err := p.client.newContainerURL(p.container)
	if err != nil {
		return nil, err
	}
	ctx := context.TODO()
	for m := (azblob.Marker{}); m.NotDone(); {
		resp, err := cURL.ListBlobsFlatSegment(ctx, m, azblob.ListBlobsSegmentOptions{Prefix: p.key})
		if err != nil {
			return nil, nil
		}
		for _, item := range resp.Segment.BlobItems {
			paths = append(paths, &AzureBlobPath{
				client:    p.client,
				container: p.container,
				key:       item.Name,
			})
		}
		m = resp.NextMarker

	}
	return paths, nil
}
