-- Core User Role Table
CREATE TABLE IF NOT EXISTS core_user_role
(
    id         bigserial    constraint role_pkey primary key,
    created_at timestamp(6) with time zone,
    deleted_at timestamp(6) with time zone,
    updated_at timestamp(6) with time zone,
    role_name  varchar(255) not null,
    role_level bigint       not null
);

INSERT INTO core_user_role (created_at, deleted_at, updated_at, role_name, role_level)
VALUES
    (now(), null, now(), 'SUPER_USER', 1),
    (now(), null, now(), 'ADMIN', 2),
    (now(), null, now(), 'USER', 3)
ON CONFLICT DO NOTHING;

-- Core User Table
CREATE TABLE IF NOT EXISTS core_user
(
    id               bigserial    constraint core_user_pkey primary key,
    created_at       timestamp(6) with time zone,
                                      deleted_at       timestamp(6) with time zone,
                                      updated_at       timestamp(6) with time zone,
                                      role_id          bigint       not null,
                                      name             varchar(255) not null,
    email            varchar(255) not null unique,
    password         varchar(255) not null,
    status           varchar(255) not null,
    uuid             varchar(255) default gen_random_uuid()
    );

INSERT INTO core_user (created_at, updated_at, role_id, name, email, password, status)
VALUES
    (now(), now(), 1, 'dyatmarize', 'dyatmarize@cool.app', '$2a$10$w0bj4LBOgzZeH7lIoF2Wae8fuUWSu6eXFjoR6BZzOWioCLB6GvRmO', 'ACTIVE'),
    (now(), now(), 2, 'admin', 'admin@cool.app', '$2a$10$huZLni..OVCjCl8T..f/yOdSOBlNmIW.vGeeoQvgeGyr0rUk09f9.', 'ACTIVE')
ON CONFLICT DO NOTHING ;