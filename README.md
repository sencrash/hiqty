hiqty
=====

A high quality [Discord](https://discordapp.com/) music bot.

Why?
----

Most (all?) existing music bots I know of are merely modules on a bigger, general-purpose bot, or are proof of concept type things more than anything. By making a dedicated music bot, with no other functionality, I'm hoping to be able to focus on doing one thing, and doing it right.

On the technical side, most wrap [Youtube-DL](https://github.com/rg3/youtube-dl) to handle URL resolution and downloads. While that is an excellent application, subprocessing it adds a massive RAM cost (~50MB) to each concurrent track, which means large, public music bots aren't feasible, even given an unmetered internet connection. With this in mind, hiqty instead communicates with various services' APIs directly, and at most subprocesses ffmpeg.

Design
------

The bot is split into two parts, the Player and the Responder. They both run on the same Discord session, but at no point must these two communicate with each other directly; instead, the single source of truth at any given time is Redis - a design heavily inspired by [Kubernetes](https://kubernetes.io/). This has a couple of advantages.

1. It's easy to query the current state, and alternate interfaces (REST API, Web UI, CLI, ...) can be added.
   
   Rather than coordinating message passing ("somebody play track X on channel Y"), there is always a single answer to the question of "what (if anything) is channel Y playing right now?" - and, for those concerned with carrying it out (the player), the subsequent question of "is that actually the case?".

1. By having track players claim locks and record their state in redis, it becomes possible to seamlessly handle both crashes and restarts.
   
   Players claim locks for servers they're aware of (this means sharding is handled properly). These locks expire after a couple of seconds if not renewed, and locks that cannot be claimed are retried until they can.
   
   This means zero-downtime upgrades can be handled by simply letting tracks finish playing on an old instance, before the lock is released and a new instance takes over playing the next queued track. Old instances terminate after all playing tracks are done.
   
   An instance that crashes and is restarted will attempt to re-claim its old locks, which will expire after a few seconds since the old instance is no longer there to renew it. A couple of seconds of downtime before the current song is restarted isn't too bad.

Redis Schema
------------

### `hiqty:server:[ID]:playlist`

List of tracks (JSON encoded) in the current playlist, FIFO.

### `hiqty:server:[ID]:state`

Playback state of the server: `playing` or `stopped`. (This key is [watched for changes](http://redis.io/topics/notifications)).

### `hiqty:server:[ID]:player_lock`

Lock to ensure that only a single player instance is active for a server at any given time.
