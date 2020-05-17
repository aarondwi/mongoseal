FROM mongo

WORKDIR /usr/src/mongoseal

COPY setup_rs.js .
COPY setup_index.js .
COPY setup.sh .

RUN ["chmod", "+x", "./setup.sh"]
CMD ["./setup.sh"]