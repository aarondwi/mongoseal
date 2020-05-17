rs.initiate({
  "_id": "rs",
  "members": [{
    "_id": 0,
    "host": "mgo1:27017"
  },{
    "_id": 1,
    "host": "mgo2:27018"
  },{
    "_id": 2,
    "host": "mgo3:27019"
  }]
}, {
  force: true
})

