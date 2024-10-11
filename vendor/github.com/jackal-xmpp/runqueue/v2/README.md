# runqueue

[![Build Status](https://img.shields.io/endpoint.svg?url=https%3A%2F%2Factions-badge.atrox.dev%2Fjackal-xmpp%2Frunqueue%2Fbadge&style=flat)](https://actions-badge.atrox.dev/jackal-xmpp/runqueue/goto)
[![Go Report Card](https://goreportcard.com/badge/github.com/jackal-xmpp/runqueue?style=flat-square)](https://goreportcard.com/report/github.com/jackal-xmpp/runqueue)
[![Coverage](https://codecov.io/gh/jackal-xmpp/sonar/branch/master/graph/badge.svg)](https://codecov.io/gh/jackal-xmpp/runqueue)
[![Godoc](http://img.shields.io/badge/go-documentation-blue.svg?style=flat-square)](https://godoc.org/github.com/jackal-xmpp/runqueue)
[![Releases](https://img.shields.io/github/release/jackal-xmpp/runqueue/all.svg?style=flat-square)](https://github.com/jackal-xmpp/runqueue/releases)
[![LICENSE](https://img.shields.io/github/license/jackal-xmpp/runqueue.svg?style=flat-square)](https://github.com/jackal-xmpp/runqueue/blob/master/LICENSE)

### Installation
```bash
go get -u github.com/jackal-xmpp/runqueue
```

### Usage
The `runqueue` package allows to enqueue and run functions in serial order ensuring exclusive access.

### Example
```go
package main 

import (
    "fmt"
    "log"

    "github.com/jackal-xmpp/runqueue"
)

func main() {
    rq := runqueue.New("rq-1", log.Printf)

    var counter int32
    for i := 0; i < 2500; i++ {
        rq.Run(func() { counter++ })
    }
    fmt.Println(counter)
}
```

Expected output:
```
2500
```

### Contributing
- Fork it
- Create your feature branch (`git checkout -b my-new-feature`)
- Commit your changes (`git commit -am 'Add some feature'`)
- Push to the branch (`git push origin my-new-feature`)
- Create new Pull Request

### License

[Apache License 2.0](https://github.com/jackal-xmpp/runqueue/blob/master/LICENSE)
