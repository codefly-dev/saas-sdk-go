module fixturemod

go 1.27.0

require ext.example/gen/go/thing v0.0.0

replace ext.example/gen/go/thing => ../extgen
