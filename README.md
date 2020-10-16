# mongoseal

Distributed locks using mongodb, with fencing

[![Build Status](https://travis-ci.org/aarondwi/mongoseal.svg?branch=master)](https://travis-ci.org/aarondwi/mongoseal)
[![Go Report Card](https://goreportcard.com/badge/github.com/aarondwi/mongoseal)](https://goreportcard.com/report/github.com/aarondwi/mongoseal)
[![GoDoc](https://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/aarondwi/mongoseal)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Setup
----------------------
Installing:

```bash
go get -u github.com/aarondwi/mongoseal
```

Run this script below in your mongodb

```javascript
db.lock.createIndex( { "Key": 1 }, { unique: true } )
db.lock.createIndex( { "last_seen": 1 }, { expireAfterSeconds: 600 } )
```

The 1st one is required, to ensure key uniqueness

The 2nd one is used to remove old entry that are not deleted (maybe because of latency, process died, etc). The `expireAfterSeconds` should be set to duration considered safe if the lock get acquired by the 2nd or so process.

There is an optional parameter, `needRefresh`, which can auto refresh the lock just before it expired. The refresh starts at `expireAfterSeconds` - `remainingBeforeResfreshSecond`, and then loop for every `expireAfterSeconds`

Notes
-------------------------------------------------
Even though this distributed lock implementation use fencing, but fencing without application specific semantic may still fail to provide exclusivity (see comments [here](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html)).
The goal of 2 types of timeouts (database level expire and application level timeout) are different:

1. the database level expire to ensure the storage requirement does not grow unbounded. Consider setting this value to a number considered safe if 2 workers hold the locks
2. the application level timeout mainly used for generating fencing token. Here, multiple workers can still hold the locks, but you have fencing token (from `lock.Version`) to be used for checking at storage/database level.

The application's OS time should be synced with NTP, and as long as the application's time is ntp-correct, this implementation should also is

The recommendation is to use `majority` concern for write ops, and `linearizable` for read ops. Also, ensure that your mongo is a replica set, not a standalone, to guarantee HA.

Usage
--------------------------------------------------

```go
// lockExpiryTimeSecond should be set to be far more
// than required duration of a process
//
// For example, if your code gonna need 10s for processing
// and 1s to save it to db or others
// set the expiryTimeSecond to be more than 11s, preferably around 20s
// to add buffer for process pause, network delay, etc
m, err := mongoseal.New(
  mongoClientObject,
  workerUniqueId,
  mongoseal.Option{
    DBName: "mongoseal",
    CollName: "lock",
    ExpiryTimeSecond: 5,
    NeedRefresh: true,
    RemainingBeforeRefreshSecond: 1
  })
if err != nil {
  // handle the errors, failed creating connection to mongodb
}

mgolock, err := m.AcquireLock(context.TODO, "some-key")
if err != nil {
  // failed acquiring lock, maybe some others have taken it
  // or there is some error in-between
}

if mgolock.IsValid() {
  // your code goes here
  // always need to check IsValid() before starting
  // to ensure the lock doesn't expire even before the code starts
  // can't do anything if the lock becomes invalid in the middle of your code
  // see https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html

  // dont forget to use fencingToken to make your application safer
  fencingToken := mgolock.Version
}

// release the lock
// it returns nothing, as error may mean some other workers has taken the lock already
m.DeleteLock(mgolock)
```

See `main_test.go` for examples

Queries use internally
------------------------------------
**acquire_lock**:

```javascript
db.lock.update({
  key: 'random-id',
  $or: [
    {last_seen: null},
    {last_seen: {$lt: currentTime - expiryTimeSecond}}]
}, {
  $inc: {version: 1},
  $set: {
    'owner': 'me',
    "last_seen": currentTime
  }
},
{upsert: true})
```

**get_lock_data**:

```javascript
db.lock.find({key: 'random-id', 'owner': 'me'}, {_id: 0})
```

**delete_lock**:

```javascript
db.lock.remove({key: 'random-id', 'owner': 'me', version: 1})
```

**refresh_lock**:

```javascript
db.lock.update(
  {key: 'random-id', 'owner': 'me', version: 1},
  {$inc: {last_seen: expiryTimeSecond}}
)
```

Document Schema
-------------------------

```json
{
  "version": 1,
  "owner": "random id you should supply to the mongoseal instance, and need to be different for each instance",
  "key": "unique id for your resource",
  "last_seen": "to check for expiry (probably stale, but other workers may still assume they have it)"
}
```
