package metering

// Version identifies this metering protocol build. It travels to Odoo in the
// X-Betta-Metering-Version header of every authorize and usage call, and the
// reseller panel compares it with the version it expects, so an outdated
// OdooClaw shows up without logging into the server. Bump it with every
// change to this package.
const Version = "2026-10-28.1"

// VersionHeader carries Version on every request to Odoo.
const VersionHeader = "X-Betta-Metering-Version"
