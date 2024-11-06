package multiset

import (
	"testing"
)

func TestAddRemove(t *testing.T) {
	set := New()
	set.Add([]byte("The"))
	// set = [The]
	if hash := set.Hash().String(); hash != "0xdc1e608f9aaaa215a3f3b87fbdfd398bd07cf0849440673c60d20fea9585035f" {
		t.Fatal("Hash mismatch:", hash)
	}
	// set = [The,quick]
	set.Add([]byte("quick"))
	if hash := set.Hash().String(); hash != "0x3e7044c8e3cf5ebbfed510ae659289e16abe55921fdadda74ab46b528142700a" {
		t.Fatal("Hash mismatch:", hash)
	}
	// set = [quick]
	set.Remove([]byte("The"))
	if hash := set.Hash().String(); hash != "0xf9baf7a2647ca56d5db8aa4a6c0e4d3b32f248ba021e6efe8bf3528f167063dc" {
		t.Fatal("Hash mismatch:", hash)
	}
	set.Add([]byte("The"))
	set.Remove([]byte("quick"))
	// set = [The]
	if hash := set.Hash().String(); hash != "0xdc1e608f9aaaa215a3f3b87fbdfd398bd07cf0849440673c60d20fea9585035f" {
		t.Fatal("Hash mismatch:", hash)
	}
}

func TestClone(t *testing.T) {
	set1 := New()
	set1.Add([]byte("The quick brown fox"))
	set2 := set1.Clone()
	if hash := set1.Hash().String(); hash != "0xb56e1a0d2963836af3dd9eb6aec412cbe3a8e1f61c5c01e72605d2a57677070a" {
		t.Fatal("Hash mismatch:", hash)
	}
	if hash := set2.Hash().String(); hash != "0xb56e1a0d2963836af3dd9eb6aec412cbe3a8e1f61c5c01e72605d2a57677070a" {
		t.Fatal("Hash mismatch:", hash)
	}
}

func TestSerdes(t *testing.T) {
	set1 := New()
	set1.Add([]byte("The quick brown fox"))
	set2, err := FromBytes(set1.Serialize())
	if err != nil {
		t.Fatal("Decode error:", err)
	}
	if hash := set1.Hash().String(); hash != "0xb56e1a0d2963836af3dd9eb6aec412cbe3a8e1f61c5c01e72605d2a57677070a" {
		t.Fatal("Hash mismatch:", hash)
	}
	if hash := set2.Hash().String(); hash != "0xb56e1a0d2963836af3dd9eb6aec412cbe3a8e1f61c5c01e72605d2a57677070a" {
		t.Fatal("Hash mismatch:", hash)
	}
}
