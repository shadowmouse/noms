// Copyright 2016 Attic Labs, Inc. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/attic-labs/noms/go/datas"
	"github.com/attic-labs/noms/go/spec"
	"github.com/attic-labs/noms/go/types"
	"github.com/attic-labs/noms/go/util/clienttest"
	"github.com/stretchr/testify/suite"
)

func TestFetch(t *testing.T) {
	suite.Run(t, &testSuite{})
}

type testSuite struct {
	clienttest.ClientTestSuite
}

func (s *testSuite) TestImportFromStdin() {
	assert := s.Assert()

	oldStdin := os.Stdin
	newStdin, blobOut, err := os.Pipe()
	assert.NoError(err)

	os.Stdin = newStdin
	defer func() {
		os.Stdin = oldStdin
	}()

	go func() {
		blobOut.Write([]byte("abcdef"))
		blobOut.Close()
	}()

	dsName := spec.CreateValueSpecString("nbs", s.DBDir, "ds")
	// Run() will return when blobOut is closed.
	s.MustRun(main, []string{"--stdin", dsName})

	sp, err := spec.ForPath(dsName + ".value")
	assert.NoError(err)
	defer sp.Close()

	ds := sp.GetDatabase().GetDataset("ds")

	expected := types.NewBlob(ds.Database(), bytes.NewBufferString("abcdef"))
	assert.True(expected.Equals(sp.GetValue()))

	meta := ds.Head().Get(datas.MetaField).(types.Struct)
	// The meta should only have a "date" field.
	metaDesc := types.TypeOf(meta).Desc.(types.StructDesc)
	assert.Equal(1, metaDesc.Len())
	assert.NotNil(metaDesc.Field("date"))
}

func (s *testSuite) TestImportFromFile() {
	assert := s.Assert()

	f, err := ioutil.TempFile("", "TestImportFromFile")
	assert.NoError(err)

	f.Write([]byte("abcdef"))
	f.Close()

	dsName := spec.CreateValueSpecString("nbs", s.DBDir, "ds")
	s.MustRun(main, []string{f.Name(), dsName})

	sp, err := spec.ForPath(dsName + ".value")
	assert.NoError(err)
	defer sp.Close()

	ds := sp.GetDatabase().GetDataset("ds")

	expected := types.NewBlob(ds.Database(), bytes.NewBufferString("abcdef"))
	assert.True(expected.Equals(sp.GetValue()))

	meta := ds.Head().Get(datas.MetaField).(types.Struct)
	metaDesc := types.TypeOf(meta).Desc.(types.StructDesc)
	assert.Equal(2, metaDesc.Len())
	assert.NotNil(metaDesc.Field("date"))
	assert.Equal(f.Name(), string(meta.Get("file").(types.String)))
}

func (s *testSuite) TestImportFromURL() {
	assert := s.Assert()
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "abcdef")
	}))
	defer svr.Close()

	dsName := spec.CreateValueSpecString("nbs", s.DBDir, "ds")
	s.MustRun(main, []string{svr.URL, dsName})

	sp, err := spec.ForPath(dsName + ".value")
	assert.NoError(err)
	defer sp.Close()

	ds := sp.GetDatabase().GetDataset("ds")

	expected := types.NewBlob(ds.Database(), bytes.NewBufferString("abcdef"))
	assert.True(expected.Equals(sp.GetValue()))

	meta := ds.Head().Get(datas.MetaField).(types.Struct)
	metaDesc := types.TypeOf(meta).Desc.(types.StructDesc)
	assert.Equal(2, metaDesc.Len())
	assert.NotNil(metaDesc.Field("date"))
	assert.Equal(svr.URL, string(meta.Get("url").(types.String)))
}

func (s *testSuite) TestImportFromURLStoresEtag() {
	assert := s.Assert()
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", "xyz123")
		fmt.Fprint(w, "abcdef")
	}))
	defer svr.Close()

	dsName := spec.CreateValueSpecString("nbs", s.DBDir, "ds")
	s.MustRun(main, []string{svr.URL, dsName})

	sp, err := spec.ForPath(dsName + ".value")
	assert.NoError(err)
	defer sp.Close()

	ds := sp.GetDatabase().GetDataset("ds")

	expected := types.NewBlob(ds.Database(), bytes.NewBufferString("abcdef"))
	assert.True(expected.Equals(sp.GetValue()))

	meta := ds.Head().Get(datas.MetaField).(types.Struct)
	metaDesc := types.TypeOf(meta).Desc.(types.StructDesc)
	assert.Equal(3, metaDesc.Len())
	assert.NotNil(metaDesc.Field("date"))
	assert.Equal(svr.URL, string(meta.Get("url").(types.String)))
	assert.Equal("xyz123", string(meta.Get("etag").(types.String)))
}

func (s *testSuite) TestImportFromURLUsesEtag() {
	assert := s.Assert()
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "xyz123" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Etag", "xyz123")
		fmt.Fprint(w, "abcdef")
	}))
	defer svr.Close()

	dsName := spec.CreateValueSpecString("nbs", s.DBDir, "ds")

	// First fetch commits and stores etag
	s.MustRun(main, []string{svr.URL, dsName})
	heightAfterFetch1 := s.commitHeight(dsName)

	// Second fetch should use etag and will not commit
	s.MustRun(main, []string{svr.URL, dsName})
	heightAfterFetch2 := s.commitHeight(dsName)

	assert.Equal(heightAfterFetch1, heightAfterFetch2)
}

func (s *testSuite) TestImportFromURLCommitsMultiple() {
	assert := s.Assert()
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "abcdef")
	}))
	defer svr.Close()

	dsName := spec.CreateValueSpecString("nbs", s.DBDir, "ds")

	// First fetch commits
	s.MustRun(main, []string{svr.URL, dsName})
	heightAfterFetch1 := s.commitHeight(dsName)

	// Second fetch also commits since there is no etag
	s.MustRun(main, []string{svr.URL, dsName})
	heightAfterFetch2 := s.commitHeight(dsName)

	assert.NotEqual(heightAfterFetch1, heightAfterFetch2)
}

func (s *testSuite) commitHeight(dsName string) uint64 {
	sp, err := spec.ForPath(dsName + ".value")
	s.Assert().NoError(err)
	ds := sp.GetDatabase().GetDataset("ds")
	defer sp.Close()
	return ds.HeadRef().Height()
}
