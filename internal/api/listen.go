package api

// DefaultListenAddr is where `any run` binds unless the config, ANY_LISTEN_ADDR
// or --addr names another address, and where a CLI call connects when neither
// --addr nor a recorded server.addr does.
const DefaultListenAddr = "127.0.0.1:2046"
