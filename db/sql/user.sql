-- name: FindUserBySteamID :one
select * from users where users.steam_id = $1 limit 1;

-- name: AddUserIgnoreConflict :exec
insert into users (steam_id)
values ($1)
on conflict (steam_id) do nothing;

-- name: AddSession :one
with target_user as (
    select u.id from users u where u.steam_id = ($1)
)
insert into session (user_id)
select tu.id from target_user tu
returning *;

-- name: DisallowToken :exec
insert into disallow_token (token_id) values ($1);

-- name: GetDisallowToken :one
select exists(select * from disallow_token where token_id = $1);
