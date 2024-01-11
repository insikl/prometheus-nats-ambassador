# Build CI Files

> **NOTE:** WIP - Placeholder files as CI testing requires a NATS server
> running with access setup.

## Files used for CI testing.

 - `user.creds`
   - NATS NKeys creds files
 - `sub_empty.json`
   - Empty JSON array start up test

 - `sub_test.json`
   - Test scrape example host of `testing.self:80` and `testing.ncsi:80`.
   - Example `cURL` commands:
     ```
     curl -v \
       --header "host: testing.self:80" \
       --header "x-prometheus-scrape-timeout-seconds: 10" \
       'http://localhost:8080/proxy'
     ```
