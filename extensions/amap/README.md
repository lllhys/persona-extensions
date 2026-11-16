# AMap (高德地图)

Places, routes and weather in China for the Persona assistant, from the AMap Web Service API with your own key. It can search places, show what is nearby (distance, phone, opening hours, rating, cost), turn an address into coordinates and back, plan driving, walking, cycling, e-bike and public transport routes (distance, time, tolls, taxi fare estimate, lines and stops), and read a Chinese city's weather. Coordinates in and out are WGS-84, like Persona's phone location and saved places. Answers are cached and paced, and a used-up allowance pauses its group and says so.

- [Extension guide, key and quota](../../docs/extensions/amap.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

```sh
go -C extensions/amap test -race -count=1 ./...
scripts/package.sh amap
```

The tests run against a fake AMap (`fake_test.go`) with scrubbed recordings of public landmarks and need no network or key.
