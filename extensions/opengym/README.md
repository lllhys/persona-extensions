# openGym

Your self-hosted [openGym](https://opengym.ch) server (the iPhone and Apple Watch workout log with an AI coach) in Persona: a 健身 page with this week's sessions and volume, the next planned session, the weekly volume trend, recent workouts with their sets and body weight; an overview card; and assistant tools that read workouts, plans and body weight, and log a workout, create a routine or change a day's plan after a confirmation on the phone.

- [Extension guide and configuration](../../docs/extensions/opengym.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

```sh
go -C extensions/opengym test -race -count=1 ./...
scripts/package.sh opengym
```

The tests run against a fake openGym server (`fake_test.go`) with synthetic data and need no network or account.
