create extension if not exists moddatetime;
create extension if not exists pgcrypto;

/*
Example soft-delete query using deleted_record table:

    with deleted AS (
        delete from users
        where id = ?
        returning *
    )
    insert into deleted_record(source_table, source_id, data)
    select 'users', id, to_jsonb(deleted.*)
    from deleted
    returning *;

alternatively, we can create a function:

    create function deleted_record_insert() returns trigger
        language plpgsql
    as $$
        begin
            execute 'insert into deleted_record (data, object_id, table_name) values ($1, $2, $3)'
            using to_jsonb(old.*), old.id, TG_TABLE_NAME;

            return old;
        end;
    $$;

and add an after delete trigger:

    create trigger deleted_record_insert after delete on my_table
        for each row execute function deleted_record_insert();

*/
create table if not exists deleted_record
(
    id           uuid primary key     default uuidv7(),
    deleted_at   timestamptz not null default now(),
    source_table text        not null,
    source_id    text        not null,
    data         jsonb       not null
);

create table if not exists users
(
    id         serial primary key,
    created_at timestamptz not null default now(),
    steam_id   text        not null unique
);

create table if not exists session
(
    id         serial primary key,
    created_at timestamptz not null default now(),

    token_id   uuid not null unique default uuidv7(),
    user_id    int not null references users
);

create table if not exists disallow_token
(
    token_id       uuid primary key,
    created_at     timestamptz not null default now()
);

create table if not exists openid_nonce
(
    id serial primary key,
    created_at timestamptz not null default now(),

    endpoint     text not null,
    nonce_time   timestamptz not null,
    nonce_string text not null,

    unique (endpoint, nonce_string)
);