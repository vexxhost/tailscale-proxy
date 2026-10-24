# `tailscale-proxy`

This is a small daemon which uses `tsnet` to create virtual devices in your
tailnet, which by default will proxy all TCP traffic into the target system
and any ports using `https` will be using the Tailscale issued certificate.

## Configuration

### Example

In the following example, we will configure the `tailscale-proxy` to proxy
all traffic from a device named `bmc-example-server` to the IP address
`1.2.3.4` and will also proxy the ports `443` and `5900` using a Tailscale
issued certificate.

The device will also be tagged with `bmc` and `example` tags.

```yaml
endpoints:
  - hostname: bmc-example-server
    ip: 1.2.3.4
    tags:
      - bmc
      - example
    listeners:
      - port: 443
        type: tls
      - port: 5900
        type: tls
      - port: 623
        type: udp
```

### Options

#### `endpoints`

In this section, you can configure all of the endpoints that will be proxied
by the `tailscale-proxy`.

##### `hostname`

This is the hostname that will be used to register the device with Tailscale.

##### `ip`

This is the IP address that the device will be proxied to.

##### `tags`

This is a list of tags that will be assigned to the device within Tailscale.

##### `ephemeral`

This controls whether the device is registered as an ephemeral node, which
is removed from the tailnet once it goes offline. It defaults to `true`; set
it to `false` to keep long-lived devices registered while they are offline.

##### `listeners`

This is a list of ports that will be proxied by the `tailscale-proxy`, with
the type being any of the following:

- `tls`: This will proxy the port using a Tailscale issued certificate.
- `udp`: This will proxy UDP traffic to the target device.

### Environment variables

The following environment variables control how the devices authenticate and
which control server they register with:

- `TS_AUTHKEY`: An auth key used to register every device.
- `TS_OAUTH_CLIENT_SECRET`: An OAuth client secret with the `auth_keys` scope,
  used instead of `TS_AUTHKEY` when set. A single-use, pre-authorized auth key
  carrying the device tags is created for each device.
- `TS_CONTROL_URL`: The control server to register with, for example a
  Headscale server. When unset, the Tailscale control server is used.

### Headscale

To register the devices with Headscale, set `TS_CONTROL_URL` to the URL of
the Headscale server and use either a pre-authentication key in `TS_AUTHKEY`
or an OAuth client secret in `TS_OAUTH_CLIENT_SECRET`:

```shell
headscale oauth-clients create --scope auth_keys --tag tag:bmc --tag tag:example
```

```shell
export TS_CONTROL_URL=https://headscale.example.com
export TS_OAUTH_CLIENT_SECRET=hskey-client-...
```

The OAuth client secret can be used as Headscale prints it, and auth keys are
created using the API of the server in `TS_CONTROL_URL`. Every device tag must
be listed in `tagOwners` in the Headscale policy and be one of the tags of the
OAuth client, or be owned by one of them.
