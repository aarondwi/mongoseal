#!/bin/sh
sleep 30 | echo "give time to all instance to be ready"
mongo mongodb://mgo1:27017 setup_rs.js
sleep 30 | echo "give some time for cluster to initiate"
targetHost=$(mongo mgo1:27017 --quiet --eval "db.isMaster()['primary']")
mongo --host $targetHost setup_index.js