package database

import (
	"comp4321/models"
	"encoding/json"
	"sync"
	"github.com/boltdb/bolt"
	"sort"
	"fmt"
)

// The Indexer object abstracts away data structure manipulations
// for inserting web documents into the search engine database.
// The Indexer object will read the .db file in read-write mode.
// Only one Indexer object can operate on the same .db file at a time.
type Indexer struct {
	db *bolt.DB

	// Temporarily hold inverted index in memory
	tempInverted map[uint64]map[uint64]bool
	wordIdList   []uint64
	sync.Mutex
}

// Return an Indexer object from .db file
func LoadIndexer(filename string) (*Indexer, error) {
	var indexer Indexer
	var err error
	indexer.db, err = bolt.Open(filename, 0666, nil)
	if err != nil {
		return nil, err
	}

	// Ensure that all buckets exist
	indexer.db.Update(func(tx *bolt.Tx) error {
		for i := 0; i < NumTable; i++ {
			tx.CreateBucketIfNotExists(intToByte(i))
		}
		return nil
	})
	return &indexer, nil
}

// Drop all tables in database
func (i *Indexer) DropAll() {
	i.db.Update(func(tx *bolt.Tx) error {
		for i := 0; i < NumTable; i++ {
			tx.DeleteBucket(intToByte(i))
			tx.CreateBucket(intToByte(i))
		}
		return nil
	})
}

// Generic id retriever from mapping table
// Forward map table converts textual representation -> unique Id
// Inverse map table converts unique Id -> textual representation
func (i *Indexer) getId(text string, fwMapTable int, invMapTable int) (id []byte) {
	id = nil
	fw := intToByte(fwMapTable)
	inv := intToByte(invMapTable)

	i.db.View(func(tx *bolt.Tx) error {
		forwardMap := tx.Bucket(intToByte(fwMapTable))
		res := forwardMap.Get([]byte(text))
		if res != nil {
			id = make([]byte, len(res))
			copy(id, res)
		}
		return nil
	})

	if id == nil {
		i.db.Batch(func(tx *bolt.Tx) error {
			forwardMap := tx.Bucket(fw)
			uniqueId, _ := forwardMap.NextSequence()
			id = uint64ToByte(uniqueId)
			forwardMap.Put([]byte(text), id)

			invMap := tx.Bucket(inv)
			invMap.Put(id, []byte(text))

			return nil
		})
	}
	return
}

// Get the pageId for the given URL, create new one if does not exist
func (i *Indexer) getOrCreatePageId(url string) []byte {
	return i.getId(url, UrlToPageId, PageIdToUrl)
}

// Get the wordId for the given word, create new one if does not exist
func (i *Indexer) getOrCreateWordId(word string) (rv []byte) {
	rv = i.getId(word, WordToWordId, WordIdToWord)
	return
}

func (i *Indexer) updateInverted(word string, pageId []byte, tablename int) {
	wordId := i.getOrCreateWordId(word)
	wordIdUint64 := byteToUint64(wordId)
	pageIdUint64 := byteToUint64(pageId)

	// Critical section - access shared map and slice
	i.Lock()
	if i.tempInverted == nil {
		i.tempInverted = make(map[uint64]map[uint64]bool)
	}

	postingList := i.tempInverted[wordIdUint64]
	if postingList == nil {
		postingList = make(map[uint64]bool)
		i.wordIdList = append(i.wordIdList, wordIdUint64)
	}

	postingList[pageIdUint64] = true
	i.tempInverted[wordIdUint64] = postingList
	i.Unlock()
	// Non critical section
}

func (i *Indexer) FlushInverted() {
	wordIdList := i.wordIdList

	// Sort slices for sequential writes
	sort.Slice(wordIdList, func(i, j int) bool {
		return wordIdList[i] < wordIdList[j]
	})

	i.db.Update(func(tx *bolt.Tx) error {
		inverted := tx.Bucket(intToByte(InvertedTable))
		inverted.FillPercent = 1
		return nil
	})

	var wg sync.WaitGroup
	wg.Add(len(wordIdList))
	for index, id := range wordIdList {
		idBytes := uint64ToByte(id)
		fmt.Printf("Merging word %d out of %d | WordID: ", index+1, len(wordIdList))
		fmt.Println(idBytes)
		go i.db.Batch(func(tx *bolt.Tx) error {
			inverted := tx.Bucket(intToByte(InvertedTable))
			wordSet, _ := inverted.CreateBucketIfNotExists(idBytes)
			postingList := i.tempInverted[id]
			for docId, _ := range postingList {
				wordSet.Put(uint64ToByte(docId), []byte{1})
			}

			wg.Done()
			return nil
		})
	}
	wg.Wait()
}

func (i *Indexer) updateForward(word string, pageId []byte, tf int, tablename int) {
	wordId := i.getOrCreateWordId(word)
	i.db.Batch(func(tx *bolt.Tx) error {
		fw := tx.Bucket(intToByte(tablename))
		set, _ := fw.CreateBucketIfNotExists(pageId)
		set.Put(wordId, intToByte(tf))
		return nil
	})
}

// Check if the URL is present in the database
func (i *Indexer) ContainsUrl(url string) (present bool) {
	i.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(intToByte(UrlToPageId))
		val := b.Get([]byte(url))
		present = val != nil
		return nil
	})
	return
}

func (i *Indexer) setMaxTf(pageId []byte, maxTf int){
	i.db.Batch(func(tx *bolt.Tx) error {
		fwTable := tx.Bucket(intToByte(ForwardTable))
		fwTable.Put(pageId, intToByte(maxTf))
		return nil
	})
}

func (i *Indexer) getMaxTf(pageId []byte) (maxTf int) {
	i.db.View(func(tx *bolt.Tx) error {
		fwTable := tx.Bucket(intToByte(ForwardTable))
		maxTf = byteToInt(fwTable.Get(pageId))
		return nil
	})
	return
}

// Insert page into the database.
// This will update all mapping tables and indexes.
func (i *Indexer) UpdateOrAddPage(p *models.Document) {
	pageId := i.getOrCreatePageId(p.Uri)
	var wg sync.WaitGroup
	wg.Add(2 * len(p.Words))
	for word, tf := range p.Words {
		go func() {
			i.updateInverted(word, pageId, InvertedTable)
			wg.Done()
		}()
		go func() {
			i.updateForward(word, pageId, tf, ForwardTable)
			wg.Done()
		}()
	}
	wg.Wait()
	i.setMaxTf(pageId, p.MaxTf)
	i.db.Batch(func(tx *bolt.Tx) error {
		documents := tx.Bucket(intToByte(PageInfo))
		encoded, _ := json.Marshal(p)
		documents.Put(pageId, encoded)
		return nil
	})
}

// TODO
// Update adj list structure
func (i *Indexer) UpdateAdjList() {

}

// TODO
// Update term weights
func (i *Indexer) UpdateTermWeights() {

}

// TODO
// Update page rank
func (i *Indexer) UpdatePageRank() {

}

func (i *Indexer) Close() {
	i.db.Close()
}
