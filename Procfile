# $REDIS_URI is set by Stackato's redis service. Heroku has a similar environment variable, but with a different name.

web: gddo-server -static=cmd/gddo-server/static -template=cmd/gddo-server/template --db-server=$REDIS_URL
