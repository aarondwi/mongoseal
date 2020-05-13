db.lock.createIndex( { "Key": 1 }, { unique: true } )
db.lock.createIndex( { "last_seen": 1 }, { expireAfterSeconds: 600 } )
