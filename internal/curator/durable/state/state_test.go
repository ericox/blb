// Copyright (c) 2016 Western Digital Corporation or its affiliates. All rights reserved.
// SPDX-License-Identifier: MIT

package state

import (
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"

	"github.com/westerndigitalcorporation/blb/internal/core"
	pb "github.com/westerndigitalcorporation/blb/internal/curator/durable/state/statepb"
	test "github.com/westerndigitalcorporation/blb/pkg/testutil"
)

func getTestState(t *testing.T) *State {
	dir, err := ioutil.TempDir(test.TempDir(), "state_test")
	if nil != err {
		t.Fatalf("failed to create temporary directory")
	}
	return Open(filepath.Join(dir, "state"))
}

// Test basic operations of State.
func TestStateBasics(t *testing.T) {
	s := getTestState(t)
	defer s.Close()

	txn := s.WriteTxn(1)
	defer txn.Commit()

	// Curator ID.
	txn.SetCuratorID(2)
	if txn.GetCuratorID() != 2 {
		t.Fatalf("wrong curator ID returned")
	}

	// Create two partitions.
	txn.PutPartition(&pb.Partition{Id: proto.Uint32(1)})
	txn.PutPartition(&pb.Partition{Id: proto.Uint32(3)})

	// Create two blobs in partition 1.
	txn.PutBlob(core.BlobIDFromParts(1, 1), &pb.Blob{Repl: proto.Uint32(3)})
	txn.PutBlob(core.BlobIDFromParts(1, 2), &pb.Blob{Repl: proto.Uint32(3)})

	// Create two blobs in partition 3.
	txn.PutBlob(core.BlobIDFromParts(3, 1), &pb.Blob{Repl: proto.Uint32(3)})
	txn.PutBlob(core.BlobIDFromParts(3, 2), &pb.Blob{Repl: proto.Uint32(3)})

	id11 := core.BlobIDFromParts(1, 1)

	// Check partitions are created.
	partitions := txn.GetPartitions()
	if len(partitions) != 2 {
		t.Fatalf("expected to get two partitions")
	}
	if partitions[0].GetId() != 1 || partitions[1].GetId() != 3 {
		t.Fatalf("unexpected partition IDs returned")
	}
	if txn.GetPartition(3) == nil {
		t.Fatalf("created partition should be found")
	}

	// Created blob should be found.
	if txn.GetBlob(id11) == nil {
		t.Fatalf("failed to get created blob")
	}
	// Non-existing blob should not be found.
	if txn.GetBlob(core.BlobIDFromParts(1, 777)) != nil {
		t.Fatalf("got non-existing blob")
	}

	// Mark a blob as deleted.
	if berr := txn.DeleteBlob(id11, time.Now()); berr != core.NoError {
		t.Fatalf("failed to delete existing blob")
	}
	// GetBlob should not return blob that has been marked as deleted.
	if txn.GetBlob(id11) != nil {
		t.Fatalf("got blob that has been marked as deleted")
	}
	// GetBlobAll should find it.
	if txn.GetBlobAll(id11) == nil {
		t.Fatalf("GetBlobAll should return blob that has been marked as deleted")
	}
	// After we really remove it, GetBlobAll shouldn't find it.
	txn.FinishDeleteBlobs([]core.BlobID{id11})
	if txn.GetBlobAll(id11) != nil {
		t.Fatalf("GetBlobAll should not have found deleted blob")
	}
}

// Test iteration of blobs.
func TestStateBlobIterator(t *testing.T) {
	s := getTestState(t)
	defer s.Close()

	txn := s.WriteTxn(1)

	// Create two partitions.
	txn.PutPartition(&pb.Partition{Id: proto.Uint32(1)})
	txn.PutPartition(&pb.Partition{Id: proto.Uint32(3)})

	txn.PutBlob(core.BlobIDFromParts(3, 2), &pb.Blob{Repl: proto.Uint32(3)})
	txn.PutBlob(core.BlobIDFromParts(3, 1), &pb.Blob{Repl: proto.Uint32(3)})
	txn.PutBlob(core.BlobIDFromParts(1, 1), &pb.Blob{Repl: proto.Uint32(3)})
	txn.PutBlob(core.BlobIDFromParts(1, 2), &pb.Blob{Repl: proto.Uint32(3)})

	txn.Commit()

	txn = s.ReadOnlyTxn()
	defer txn.Commit()

	var iteratedIDs []core.BlobID
	for iter, ok := txn.GetIterator(0); ok; ok = iter.Next() {
		id, _ := iter.Blob()
		iteratedIDs = append(iteratedIDs, id)
	}

	// The iteration should be done by the ascending order of blob IDs.
	if !reflect.DeepEqual([]core.BlobID{
		core.BlobIDFromParts(1, 1),
		core.BlobIDFromParts(1, 2),
		core.BlobIDFromParts(3, 1),
		core.BlobIDFromParts(3, 2),
	}, iteratedIDs) {
		t.Fatalf("unexpected iteration on blob IDs")
	}
}

// Test isolation between read-write and read-only transactions.
func TestStateReadWriteIsolation(t *testing.T) {
	s := getTestState(t)
	defer s.Close()

	writeTxn := s.WriteTxn(1)
	// Create partition 1.
	writeTxn.PutPartition(&pb.Partition{Id: proto.Uint32(1)})
	// Create a blob.
	writeTxn.PutBlob(core.BlobIDFromParts(1, 1), &pb.Blob{Repl: proto.Uint32(3)})

	// Start a read txn without committing the write txn.
	readTxn := s.ReadOnlyTxn()

	// The writes should not show up.
	if readTxn.GetPartition(1) != nil {
		t.Fatalf("write showed up before commit")
	}
	if readTxn.GetBlob(core.BlobIDFromParts(1, 1)) != nil {
		t.Fatalf("write showed up before commit")
	}
	if index := readTxn.GetIndex(); index != 0 {
		t.Fatalf("index should be 0")
	}

	// Commit the write now.
	writeTxn.Commit()
	readTxn.Commit()

	// Now a read transaction after it should see committed result.
	readTxn = s.ReadOnlyTxn()
	defer readTxn.Commit()

	// The committed writes should show up.
	if readTxn.GetPartition(1) == nil {
		t.Fatalf("write didn't show up after commit")
	}
	if readTxn.GetBlob(core.BlobIDFromParts(1, 1)) == nil {
		t.Fatalf("write didn't show up after commit")
	}
	if index := readTxn.GetIndex(); index != 1 {
		t.Fatalf("index should be 1 after write txn commits")
	}
}

// Test two consecutive write transactions.
func TestMultipleWriteTxns(t *testing.T) {
	s := getTestState(t)
	defer s.Close()

	writeTxn := s.WriteTxn(1)
	// Create partition 1.
	writeTxn.PutPartition(&pb.Partition{Id: proto.Uint32(1)})
	writeTxn.Commit()

	writeTxn = s.WriteTxn(2)
	// Create a blob.
	writeTxn.PutBlob(core.BlobIDFromParts(1, 1), &pb.Blob{Repl: proto.Uint32(3)})
	writeTxn.Commit()

	// Verify that the two committed write transactions should be effective.

	readTxn := s.ReadOnlyTxn()
	defer readTxn.Commit()

	// The committed writes should show up.
	if readTxn.GetPartition(1) == nil {
		t.Fatalf("write didn't show up after commit")
	}
	if readTxn.GetBlob(core.BlobIDFromParts(1, 1)) == nil {
		t.Fatalf("write didn't show up after commit")
	}
	if index := readTxn.GetIndex(); index != 2 {
		t.Fatalf("index should be 2 after write txn commits")
	}
}

// Test updating mtime/atime.
func TestUpdateTimes(t *testing.T) {
	s := getTestState(t)
	defer s.Close()

	txn := s.WriteTxn(1)
	txn.PutPartition(&pb.Partition{Id: proto.Uint32(3)})
	txn.PutBlob(core.BlobIDFromParts(3, 1), &pb.Blob{Repl: proto.Uint32(3), Mtime: proto.Int64(200), Atime: proto.Int64(200)})
	txn.PutBlob(core.BlobIDFromParts(3, 2), &pb.Blob{Repl: proto.Uint32(3), Mtime: proto.Int64(200), Atime: proto.Int64(200)})
	txn.Commit()

	txn = s.ReadOnlyTxn()
	if b := txn.GetBlob(core.BlobIDFromParts(3, 1)); b == nil || b.GetMtime() != 200 || b.GetAtime() != 200 {
		t.Errorf("blob 3:1 has wrong times")
	}
	txn.Commit()

	txn = s.WriteTxn(2)
	txn.BatchUpdateTimes([]UpdateTime{
		{Blob: core.BlobIDFromParts(3, 1), MTime: 300, ATime: 0},
		{Blob: core.BlobIDFromParts(3, 2), MTime: 400, ATime: 0},
		{Blob: core.BlobIDFromParts(3, 2), MTime: 300, ATime: 450},
		{Blob: core.BlobIDFromParts(3, 3), MTime: 100, ATime: 100}, // doesn't exist
	})
	txn.Commit()

	txn = s.ReadOnlyTxn()
	if b := txn.GetBlob(core.BlobIDFromParts(3, 1)); b == nil || b.GetMtime() != 300 || b.GetAtime() != 200 {
		t.Errorf("blob 3:1 has wrong times")
	}
	if b := txn.GetBlob(core.BlobIDFromParts(3, 2)); b == nil || b.GetMtime() != 400 || b.GetAtime() != 450 {
		t.Errorf("blob 3:2 has wrong times")
	}
	txn.Commit()
}

func TestLookupTractInChunk(t *testing.T) {
	tid := func(p core.PartitionID, b core.BlobKey, t core.TractKey) core.TractID {
		return core.TractIDFromParts(core.BlobIDFromParts(p, b), t)
	}
	cid := core.RSChunkID{Partition: 0x80000555, ID: 0x5555}
	chunk := &pb.RSChunk{ // RS(6,3)
		Data: []*pb.RSChunk_Data{
			{Tracts: []*pb.RSChunk_Data_Tract{
				{Id: tid(123, 456, 0), Length: 100, Offset: 0},
				{Id: tid(123, 456, 1), Length: 321, Offset: 1000},
				{Id: tid(321, 654, 2), Length: 654, Offset: 2000},
				{Id: tid(321, 654, 3), Length: 987, Offset: 3000},
			}},
			{},
			{},
			{Tracts: []*pb.RSChunk_Data_Tract{
				{Id: tid(321, 654, 0), Length: 100, Offset: 0},
				{Id: tid(321, 654, 1), Length: 321, Offset: 1000},
				{Id: tid(123, 456, 2), Length: 654, Offset: 2000},
				{Id: tid(123, 456, 3), Length: 987, Offset: 3000},
			}},
			{},
			{},
		},
		Hosts: []core.TractserverID{9, 8, 7, 6, 5, 4, 3, 2, 1},
	}

	check := func(tid core.TractID, exp core.TractPointer) {
		tp, _ := lookupTractInChunk(chunk, tid, rschunkID2Key(cid), core.StorageClass_RS_6_3)
		if !reflect.DeepEqual(tp, exp) {
			t.Errorf("wrong result for %v: %+v != %+v", tid, tp, exp)
		}
	}

	// not present
	check(tid(777, 777, 777), core.TractPointer{})
	// present:
	check(tid(123, 456, 2), core.TractPointer{Chunk: cid.Add(3), Offset: 2000, Length: 654, TSID: 6,
		Class: core.StorageClass_RS_6_3, BaseChunk: cid, OtherTSIDs: chunk.Hosts})
	check(tid(321, 654, 3), core.TractPointer{Chunk: cid.Add(0), Offset: 3000, Length: 987, TSID: 9,
		Class: core.StorageClass_RS_6_3, BaseChunk: cid, OtherTSIDs: chunk.Hosts})
	check(tid(321, 654, 0), core.TractPointer{Chunk: cid.Add(3), Offset: 0, Length: 100, TSID: 6,
		Class: core.StorageClass_RS_6_3, BaseChunk: cid, OtherTSIDs: chunk.Hosts})
}

func TestLookupRSPiece(t *testing.T) {
	s := getTestState(t)
	defer s.Close()

	txn := s.WriteTxn(1)
	defer txn.Commit()

	// Add some RS chunks. They don't need to be complete or consistent, we're
	// just using Hosts here.
	p := core.PartitionID(0x80000555)
	k1 := core.RSChunkID{Partition: p, ID: 5000}
	ch1 := &pb.RSChunk{Hosts: []core.TractserverID{9, 8, 7, 6, 5, 4, 3, 2, 1}}
	txn.put(rschunkBucket, rschunkID2Key(k1), mustMarshal(ch1), defaultFillPct)

	k2 := core.RSChunkID{Partition: p, ID: 6000}
	ch2 := &pb.RSChunk{Hosts: []core.TractserverID{16, 15, 14, 13, 12, 11}}
	txn.put(rschunkBucket, rschunkID2Key(k2), mustMarshal(ch2), defaultFillPct)

	check := func(id core.RSChunkID, expID core.TractserverID, expOK bool) {
		tsid, ok := txn.LookupRSPiece(id.ToTractID())
		if ok != expOK || tsid != expID {
			t.Errorf("LookupRSPiece(%v) == %v, %v, expected %v, %v", id, tsid, ok, expID, expOK)
		}
	}
	check(core.RSChunkID{Partition: p - 1, ID: 7000}, 0, false)
	check(core.RSChunkID{Partition: p, ID: 4000}, 0, false)
	check(core.RSChunkID{Partition: p, ID: 4999}, 0, false)
	check(core.RSChunkID{Partition: p, ID: 5000}, 9, true)
	check(core.RSChunkID{Partition: p, ID: 5001}, 8, true)
	check(core.RSChunkID{Partition: p, ID: 5002}, 7, true)
	check(core.RSChunkID{Partition: p, ID: 5008}, 1, true)
	check(core.RSChunkID{Partition: p, ID: 5009}, 0, false)
	check(core.RSChunkID{Partition: p, ID: 5010}, 0, false)
	check(core.RSChunkID{Partition: p, ID: 5999}, 0, false)
	check(core.RSChunkID{Partition: p, ID: 6000}, 16, true)
	check(core.RSChunkID{Partition: p, ID: 6001}, 15, true)
	check(core.RSChunkID{Partition: p, ID: 6005}, 11, true)
	check(core.RSChunkID{Partition: p, ID: 6006}, 0, false)
	check(core.RSChunkID{Partition: p + 1, ID: 4000}, 0, false)
}

func TestDeleteTractFromRSChunk(t *testing.T) {
	s := getTestState(t)
	defer s.Close()
	txn := s.WriteTxn(1)
	defer txn.Commit()
	bid := core.BlobIDFromParts(7, 3)
	tid := core.TractIDFromParts(bid, 0)
	cid := core.RSChunkID{Partition: 0x80000007, ID: 5}
	txn.PutPartition(&pb.Partition{Id: proto.Uint32(7)})
	txn.PutBlob(bid, &pb.Blob{
		Tracts: []*pb.Tract{
			{Version: 1},
		},
	})
	hosts := []core.TractserverID{9, 8, 7, 6, 5, 4, 3, 2, 1}
	err := txn.PutRSChunk(cid, core.StorageClass_RS_6_3, hosts, [][]EncodedTract{
		{}, {}, {{ID: tid, Offset: 123, Length: 456}}, {}, {}, {}, {}, {}, {},
	})
	if err != core.NoError {
		t.Fatal(err)
	}

	// check that it's there
	c := txn.GetRSChunk(cid)
	if c.Data[2].Tracts[0].Id != tid {
		t.Fatalf("tract not present in rs chunk")
	}

	// delete the blob
	txn.FinishDeleteBlobs([]core.BlobID{bid})

	// shouldn't be there anymore
	c = txn.GetRSChunk(cid)
	if len(c.Data[2].Tracts) > 0 {
		t.Fatalf("tract is still present in rs chunk")
	}
}

func TestGetKnownTSIDs(t *testing.T) {
	s := getTestState(t)
	defer s.Close()

	tx := s.WriteTxn(1)
	tx.PutPartition(&pb.Partition{Id: proto.Uint32(1)})
	tx.PutBlob(core.BlobIDFromParts(1, 1), &pb.Blob{Repl: proto.Uint32(3),
		Tracts: []*pb.Tract{{Hosts: []core.TractserverID{1, 5, 7}}}})
	tx.Commit()

	tx = s.WriteTxn(2)
	tx.PutBlob(core.BlobIDFromParts(1, 2), &pb.Blob{Repl: proto.Uint32(3),
		Tracts: []*pb.Tract{{Hosts: []core.TractserverID{5, 7, 9}}}})
	tx.Commit()

	// TODO: should test PutRSChunk and UpdateRSHosts too

	tx = s.ReadOnlyTxn()
	ids, err := tx.GetKnownTSIDs()
	if err == core.NoError {
		t.Errorf("should not have cached tsids yet")
	}
	tx.Commit()

	tx = s.WriteTxn(3)
	tx.CreateTSIDCache()
	tx.Commit()

	tx = s.ReadOnlyTxn()
	ids, err = tx.GetKnownTSIDs()
	tx.Commit()
	if err != core.NoError {
		t.Errorf("doesn't have tsids")
	}
	if !reflect.DeepEqual(ids, []core.TractserverID{1, 5, 7, 9}) {
		t.Errorf("mismatch: %v", ids)
	}

	tx = s.WriteTxn(4)
	tx.PutBlob(core.BlobIDFromParts(1, 2), &pb.Blob{Repl: proto.Uint32(3),
		Tracts: []*pb.Tract{{Hosts: []core.TractserverID{11, 17, 9}}}})
	tx.Commit()

	tx = s.ReadOnlyTxn()
	ids, err = tx.GetKnownTSIDs()
	tx.Commit()
	if err != core.NoError {
		t.Errorf("doesn't have tsids")
	}
	if !reflect.DeepEqual(ids, []core.TractserverID{1, 5, 7, 9, 11, 17}) {
		t.Errorf("mismatch: %v", ids)
	}
}
