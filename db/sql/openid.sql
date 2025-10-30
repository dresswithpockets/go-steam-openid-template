-- name: InsertNonce :exec
insert into openid_nonce (endpoint, nonce_time, nonce_string) values ($1, $2, $3);