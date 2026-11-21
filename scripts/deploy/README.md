# Release deployment checks

`check-starter-alpine/` verifies Alpine script integrity in published Starter
bundles. It is used by the release workflow and is independent of the Go
systemd/Plesk deployment tool.

For server setup, deploying sites, backups and monitoring, use the
[Go ocmsctl deployment guide](../../docs/deploy-ubuntu-plesk.md).
The CLI, templates, systemd unit and database scrub policy live in
[`cmd/ocmsctl`](../../cmd/ocmsctl/).
