This project is the source for http://godoc.org/

[![GoDoc](https://godoc.org/github.com/garyburd/gddo?status.png)](http://godoc.org/github.com/garyburd/gddo)

The code in this project is designed to be used by godoc.org. Send mail to
info@godoc.org if you want to discuss other uses of the code.

Feedback
--------

Send ideas and questions to info@godoc.org. Request features and report bugs
using the [GitHub Issue
Tracker](https://github.com/garyburd/gopkgdoc/issues/new). 


Contributions
-------------
Contributions to this project are welcome, though please send mail before
starting work on anything major. Contributors retain their copyright, so we
need you to fill out a short form before we can accept your contribution:
https://developers.google.com/open-source/cla/individual

Development Environment Setup
-----------------------------

- Install and run [Redis 2.8.x](http://redis.io/download). The redis.conf file included in the Redis distribution is suitable for development.
- Install Go 1.2.
- Install and run the server:

        $ go get github.com/garyburd/gddo/gddo-server
        $ gddo-server

- Go to http://localhost:8080/ in your browser
- Enter an import path to have the server retrieve & display a package's documentation

Optional:

- Create the file gddo-server/config.go using the template in [gddo-server/config.go.template](gddo-server/config.go.template).

API
---

There are four API endpoints. See [gddo-server/main.go](https://github.com/garyburd/gddo/blob/8baf8dd2442efe39f7b132d20f70f73c62e2b2b7/gddo-server/main.go#L866-L869).

With the exception of the `/packages` endpoint, all package lists contain a synopsis if present in the code.

A plain text interface is documented at <http://godoc.org/-/about>.
