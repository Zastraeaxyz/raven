# Raven

Raven is the keyholder persona for the server. She is a standalone Discord bot
with her own token, commands, persona prompt, and message intents. She was split
out of [Grimoire](../grimoire) so each bot has its own repo, process, and deploy.

## What she does

- Answers when a member mentions her or replies to one of her messages.
- Reads image attachments on the current message and on the message she replies
  to, so a member can show rather than describe.
- Judges femdom, chastity, and cuckold captions, and writes them when a sub
  asks. Captions are a hobby, not a special mode.
- Exposes `/about` so anyone can meet her and learn what she enjoys.
- Any member can talk to her by default. Setting the lockee role limits her to
  members with that role.
- Can check the caller's ChastiSafe lock status with a tool, only when locks or
  chastity are already part of the conversation.
- Can add time as a punishment and freeze or thaw a timed lock.
- Remembers each member and the server with short-term and long-term notes, and
  compacts those files when they grow too long.
- Runs from a live `SOUL.md` on the data volume. `/learn` lets an admin teach
  new behavior and approve a SOUL change without a deployment.
- Exposes `/memory view` and `/memory forget` so a member controls their own
  memory.
- Exposes `/chastity status` so a member can check their own lock privately.
- Exposes `/prompt` to admins so the live SOUL can be reviewed.

## Setup

1. Create a Discord application and bot.
2. In the Bot settings, enable the **Message Content** intent. Nothing else is
   required for the gateway.
3. Invite the bot with the `bot` and `applications.commands` scopes.
4. Set `DISCORD_TOKEN` and `OPENROUTER_API_KEY` on the service.
5. Optionally set `GUILD_ID` to restrict the persona to one server. Empty serves
   every guild she is invited to.
6. Optionally create a lockee role and set `LOCKEE_ROLE_ID` to its ID. Without
   it, any member can talk to the persona.
7. Optionally edit `prompts/keyholder.md` and `prompts/about.md`. The SOUL file
   is seeded from `keyholder.md` on first boot; after that the live `SOUL.md` on
   the data volume wins.

The persona needs her own Discord identity. The username and avatar live in the
Discord Developer Portal, not in this repository.

Without `OPENROUTER_API_KEY` or a prompt, startup fails.

## Images

Image attachments on the mentioning message and on the message being replied to
are passed to the model as multimodal content, with a short label that names
the file. That lets a member send a picture with no text at all.

Only images Discord hosts are used, so the model never fetches an arbitrary
link. Accepted types are PNG, JPEG, WebP, and GIF. At most four images go per
request, and each must be 8 MB or smaller. History stays text-only; images from
older messages are not re-sent.

The configured model must support vision. With a text-only slug the request
fails and Raven answers that she lost her train of thought, so point
`OPENROUTER_MODEL` at a vision-capable model. Turn the feature off with
`VISION_ENABLED=false`.

## Captions

Captions are part of her persona. Femdom, FLR, chastity, and cuckold captions
are her hobby, and she treats them as craft. A caption reaction is kept to one
tweet, under 280 characters, with no score. She never describes what the image
shows, because a misread would break the scene. She may talk about the idea in
the text, but she paraphrases it instead of quoting it.

When a caption earns a reaction, she adds one emoji to the image message through
the `caption_react` tool, not to her reply: ❤️ for a good caption, 😍 when it
earns more than a heart, 😈 for a mean or strict keyholder, 🥵 for forced-bi or
cleaning up a bull's cum from her pussy, and ❤️‍🔥 for a wholesome but still
strict keyholder. Raven needs the **Add Reactions** permission in the channel
for this to work.

A member can ask for more and get the full critique: the fantasy, the writing,
the tease, and the fix, still without describing the picture or quoting the
text. She also writes captions on request and hands out themes for subs to hunt
down. Captions stay consensual fiction and inside the limits of the people in
the scene.

The behavior starts in `prompts/keyholder.md`. The live copy is `SOUL.md` on
the data volume, and `/learn` edits that copy.

## Memory and SOUL

The live persona is `SOUL.md` on the data volume. `MEMORY_DIR` defaults to
`./data`, which is `/data` on Railway. On first boot the file is seeded from
`SYSTEM_PROMPT_FILE` or the inline `SYSTEM_PROMPT`. After that the volume copy
wins, so learning edits survive deploys. `/soul reset` seeds it again from the
repository file.

Memory is markdown under the same directory:

```
SOUL.md
SOUL-history/
memory/server.md
memory/users/<discord_id>.md
memory/history/
```

Each member file has `## Long-term` and `## Short-term` sections. Long-term
holds durable facts, preferences, limits, relationship state, and recurring
themes. Short-term holds recent dated notes and open threads. After each reply a
background clerk extracts a few key points and appends them. Full transcripts
are never stored. When a file passes `MEMORY_MAX_CHARS`, it is compacted into a
shorter version and the old file is kept under `memory/history`.

The current member's memory and the shared server memory are added to the system
prompt as reference data, never as instructions. `/memory view` shows a member
their own file. `/memory forget confirm:true` deletes it. Admins can target
another member with the `user` option and list files with `/memory list`.

## Learning mode

Run `/learn start` in a private admin channel. The session lasts 60 minutes by
default, and the developer can talk normally without mentioning the bot. Raven
uses a meta prompt, discusses the lesson, and can call `soul_update` with a full
revised SOUL. The bot posts the proposal with **Approve** and **Reject** buttons
and attaches the proposed file.

Approve writes `SOUL.md`, reloads it immediately, and keeps the previous version
for `/soul undo`. Reject changes nothing. Sessions expire automatically and
`/learn stop` ends one early.

The safety rules are appended from code after the editable SOUL. Learning mode
can change her voice, hobbies, and behavior, but it cannot remove consent,
limits, safewords, or the rule against claiming power she does not have.

## ChastiSafe

The persona acts as one ChastiSafe keyholder account. Put that account's
developer code in `CHASTISAFE_KEYHOLDER_CODE`.

The developer code is a password. It is sent in the `chastisafedev` header,
never logged, and never written to disk. Raven uses this one account and never
reads member codes.

Where to find the code: sign in to ChastiSafe, open your profile, and copy the
developer code shown there.

The model sees the lock status as a tool. It is not injected into every prompt,
so she only looks when the conversation makes it relevant. Most conversations
never touch ChastiSafe.

### Connecting a lockee

ChastiSafe does not let a keyholder attach a lockee through its API. The lockee
starts the relationship:

1. Create at least one timed lock template in the persona's ChastiSafe account
   so a lockee has something to load. Enable updates on it so punishments can
   add time. The bot does not create templates.
2. The member runs `/chastity status`.
3. With no active lock, the bot replies with its keyholder name, ChastiSafe ID,
   and the templates it offers.
4. The member adds that account as their keyholder in the ChastiSafe app and
   loads a timed lock.
5. The bot sees the lock the next time the member talks to it.

Recognition uses the Discord ID linked to the ChastiSafe account, so there is
no separate allowlist to maintain.

## Commands

| Command | Who | Result |
| --- | --- | --- |
| `/about` | Any member | Public dating-profile style introduction, including her caption hobby |
| `/memory view` | Any member | Ephemeral copy of the caller's memory file |
| `/memory forget` | Any member | Deletes the caller's memory after `confirm:true`; admins can target another member |
| `/memory list` | Admins | Ephemeral list of memory files and sizes |
| `/learn start`, `/learn stop`, `/learn status` | Admins | Interactive SOUL tuning session in the current channel |
| `/soul undo` | Admins | Restores the previous SOUL version |
| `/soul reset` | Admins | Resets the SOUL to the repository seed |
| `/chastity status` | Any member | Ephemeral list of the caller's active locks, or connection instructions |
| `/prompt` | Admins | Ephemeral copy of the live SOUL, safety rules, and tool guidance |
| Mentions and replies | Any member allowed to talk to her | An in-character reply; lock status is available as a tool, not injected |

Admins can run `/chastity status` because slash command permissions always allow
server admins.

## Punishment

Raven has house rules in her prompt, starting with the rule that no sub may
change his mind without her permission. When a rule breaks, she can call
`chastity_punish`, which adds time to the sub's timed lock.

The amount is 5% of the remaining time, clamped to 30 minutes at the low end and
4 hours at the high end. A two hour lock takes a 30 minute hit. A two day lock
takes about 2 hours 24 minutes. Tune it with the variables below.

Raven can also freeze or thaw a timed lock. She cannot unlock a lock, create
one, or change a card lock.

## Limits

This version only touches timed locks, and only through three tools.

- It can read status, add time, and freeze or thaw a timed lock.
- It cannot unlock, remove cards, create a lock, assign a task, or start a
  pillory.
- The system prompt tells the persona this explicitly, so it does not pretend
  to have controls it lacks.
- Lock combinations and unlock codes are never placed in the prompt, the reply,
  or the logs.
- Status is a tool, not a prompt injection. The model sees it in its toolbelt
  and calls it only when chastity or locks are already in the conversation.
- Status responses are ephemeral. Channel mentions do not quote private lock
  details beyond what the persona chooses to say.
- Message content is never logged. Only channel, member, timing, and token
  counts are recorded.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `DISCORD_TOKEN` | | Bot token |
| `OPENROUTER_API_KEY` | | Required at startup |
| `GUILD_ID` | all guilds | Optional server lock |
| `PORT` | `8080` | Health server port |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `BOT_NAME` | `Keyholder` | Fills `{{bot}}` in the prompt |
| `LOCKEE_ROLE_ID` | | Optional role gate; empty allows any member |
| `SYSTEM_PROMPT_FILE` | `./prompts/keyholder.md` | Seed for the live SOUL on first boot |
| `SYSTEM_PROMPT` | | Inline seed; the live SOUL wins after first boot |
| `ABOUT_FILE` | `./prompts/about.md` | Text served by `/about` |
| `CONTEXT_MESSAGES` | `15` | Channel history per request |
| `MAX_CHUNKS` | `3` | Reply split limit |
| `TIMEOUT_SECONDS` | `60` | Model request timeout |
| `MAX_CONCURRENT` | `2` | Concurrent model calls |
| `RATE_LIMIT_CAPACITY` | `2` | Requests per member per window |
| `RATE_LIMIT_SECONDS` | `10` | Window length |
| `VISION_ENABLED` | `true` | Read image attachments |
| `VISION_MAX_IMAGES` | `4` | Images per request |
| `VISION_MAX_IMAGE_MB` | `8` | Largest image read |
| `VISION_DETAIL` | `auto` | `auto`, `low`, `high`, or `original` |
| `MEMORY_ENABLED` | `true` | Persistent per-member and server memory |
| `MEMORY_DIR` | `./data` | SOUL and memory directory; must be on the volume |
| `MEMORY_MAX_CHARS` | `3000` | Memory file size before compaction |
| `MEMORY_CONTEXT_MAX_CHARS` | `2500` | Memory characters injected per request |
| `MEMORY_TIMEOUT_SECONDS` | `30` | Background memory capture timeout |
| `SOUL_MAX_CHARS` | `8000` | Largest editable SOUL |
| `LEARNING_SESSION_MINUTES` | `60` | Lifetime of a `/learn` session |
| `LEARNING_TIMEOUT_SECONDS` | `60` | Learning model request timeout |
| `AUX_MODEL` | main model | Model for memory, compaction, and learning |
| `AUX_MAX_TOKENS` | `4096` | Token cap for the auxiliary model |
| `PUNISH_PERCENT` | `5` | Percent of remaining time added per punishment |
| `PUNISH_MIN_MINUTES` | `30` | Smallest punishment |
| `PUNISH_MAX_MINUTES` | `240` | Largest punishment |
| `OPENROUTER_MODEL` | `deepseek/deepseek-v4.1-flash-1017` | Model slug |
| `OPENROUTER_BASE_URL` | `https://openrouter.ai/api/v1` | API root |
| `CHASTITY_ENABLED` | `true` | ChastiSafe status and keyholder tools |
| `CHASTISAFE_KEYHOLDER_CODE` | | ChastiSafe keyholder developer code |
| `CHASTISAFE_BASE_URL` | `https://chastisafe.com/api-v1` | API root override |

A missing keyholder code disables only the ChastiSafe status tool and the rest
of the persona keeps running. A bad code logs a warning at startup and disables
the tool for that process.

## Running locally

```sh
DISCORD_TOKEN=... \
OPENROUTER_API_KEY=... \
CHASTISAFE_KEYHOLDER_CODE=... \
go run .
```

The persona can be tested against a test ChastiSafe keyholder account before the
real one is connected.

## Tests

```sh
go test ./...
```

## Railway

The `Dockerfile` builds a static binary and drops to uid 10001. Mount a volume
at `/data` and keep `MEMORY_DIR=/data`; the entrypoint fixes volume ownership on
boot. `mise deploy` runs `railway up`.

## Safety

This is a consensual adults-only space. The persona is instructed to treat
limits and safewords as absolute and to answer plainly when a scene stops. Since
this version cannot unlock a lock, a safeword needs no automated release path
yet. A lockee must always keep an independent way out, such as the ChastiSafe
app or a physical key. A Discord bot should never be the only route to release.
