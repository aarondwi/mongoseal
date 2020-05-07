package mgofencedlock

import (
	"context"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MgoLock is the object users get
// after lock is acquired at mongodb
type MgoLock struct {
	sync.Mutex
	Key        string
	Version    int
	isValid    bool
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// IsValid handle goroutine-safe checking of isValid
func (m *MgoLock) IsValid() bool {
	m.Lock()
	defer m.Unlock()
	return m.isValid
}
func (m *MgoLock) updateValidity(status bool) {
	m.Lock()
	defer m.Unlock()
	m.isValid = status
}

// MgoFencedLock is the core object to create by user
type MgoFencedLock struct {
	client           *mongo.Client
	lockColl         *mongo.Collection
	ctx              context.Context
	cancelFunc       context.CancelFunc
	ownerID          string
	expiryTimeSecond int64
}

// New creates our new MgoFencedLock
func New(
	connectionURL string,
	dbname string,
	ownerID string,
	expiryTimeSecond int64) (*MgoFencedLock, error) {
	ctx, cancelFunc := context.WithCancel(context.Background())

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionURL))
	if err != nil {
		log.Fatalf("Failed connecting to mongo: %v", err)
		cancelFunc()
		return nil, err
	}

	coll := client.Database(dbname).Collection("lock")
	return &MgoFencedLock{
		client:           client,
		lockColl:         coll,
		ctx:              ctx,
		cancelFunc:       cancelFunc,
		ownerID:          ownerID,
		expiryTimeSecond: expiryTimeSecond,
	}, nil
}

// Close the connection to mongo
func (m *MgoFencedLock) Close() {
	if m.client != nil {
		m.client.Disconnect(m.ctx)
	}
	m.cancelFunc()
}

// AcquireLock creates lock records on mongodb
// and fetch the record to return to users
func (m *MgoFencedLock) AcquireLock(key string) (*MgoLock, error) {
	currentTime := time.Now().Unix()
	filter := bson.D{
		bson.E{Key: "Key", Value: key},
		bson.E{
			Key: "$or",
			Value: bson.A{
				bson.D{bson.E{Key: "last_seen", Value: nil}},
				bson.D{bson.E{Key: "last_seen",
					// resolution unit is second
					// reducing the chance from ntp ~250ms error
					Value: bson.M{"$lt": currentTime - m.expiryTimeSecond}}},
			}},
	}
	update := bson.D{
		bson.E{Key: "$inc", Value: bson.M{"Version": 1}},
		bson.E{
			Key: "$set",
			Value: bson.D{
				bson.E{Key: "owner", Value: m.ownerID},
				bson.E{Key: "last_seen", Value: currentTime},
			}},
	}
	_, err := m.lockColl.UpdateOne(
		m.ctx, filter, update,
		options.Update().SetUpsert(true))

	if err != nil {
		log.Printf("Failed Upserting lock into mongo: %v", err)
		log.Print(currentTime)
		log.Print(currentTime - m.expiryTimeSecond)
		return nil, err
	}

	var mgolock MgoLock
	ctx, cancelFunc := context.WithCancel(m.ctx)
	mgolock.ctx = ctx
	mgolock.cancelFunc = cancelFunc

	filter = bson.D{
		bson.E{Key: "owner", Value: m.ownerID},
		bson.E{Key: "Key", Value: key}}
	err = m.lockColl.FindOne(mgolock.ctx, filter).Decode(&mgolock)
	if err != nil {
		log.Printf("Just written lock not found %v", err)
		return nil, err
	}

	mgolock.updateValidity(true)
	go m.refreshLock(&mgolock, m.expiryTimeSecond)
	return &mgolock, nil
}

func (m *MgoFencedLock) refreshLock(mgolock *MgoLock, expiryTimeSecond int64) {
	// 100ms before the lock is considered stale, we refresh
	// also act as buffer to reduce margin of error
	ticker := time.NewTicker(
		time.Duration((expiryTimeSecond*1000)-100) *
			time.Millisecond)

	for {
		select {
		case <-mgolock.ctx.Done():
			mgolock.updateValidity(false)
			return
		case <-ticker.C:
			log.Printf("Refreshing the lock with key: %s", mgolock.Key)
			filter := bson.D{
				bson.E{Key: "owner", Value: m.ownerID},
				bson.E{Key: "Key", Value: mgolock.Key},
				bson.E{Key: "Version", Value: mgolock.Version}}
			update := bson.D{
				bson.E{
					Key: "$set",
					Value: bson.E{
						Key:   "last_seen",
						Value: time.Now().Unix()}}}
			result, err := m.lockColl.UpdateOne(mgolock.ctx, filter, update)
			if err != nil ||
				(result.MatchedCount == 0 &&
					result.ModifiedCount == 0 &&
					result.UpsertedCount == 0) {
				mgolock.updateValidity(false)
				return
			}
		}
	}
}

// DeleteLock removes the record lock from mongodb
// Returns nothing, as error may mean the lock has been taken by others
func (m *MgoFencedLock) DeleteLock(mgolock *MgoLock) {
	if mgolock.IsValid() {
		mgolock.updateValidity(false)
		filter := bson.D{
			bson.E{Key: "owner", Value: m.ownerID},
			bson.E{Key: "Key", Value: mgolock.Key},
			bson.E{Key: "Version", Value: mgolock.Version}}
		m.lockColl.DeleteOne(mgolock.ctx, filter)
	}
	mgolock.cancelFunc()
}
