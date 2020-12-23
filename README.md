Timeliner [![timeliner godoc](https://pkg.go.dev/badge/github.com/mholt/timeliner)](https://pkg.go.dev/github.com/mholt/timeliner)
=========

Timeliner is a personal data aggregation utility. It collects all your digital things from pretty much anywhere and stores them on your own computer, indexes them, and projects them onto a single, unified timeline.

The intended purpose of this tool is to help preserve personal and family history.

Things that are stored by Timeliner are easily accessible in a SQLite database or, for media items like photos and videos, are simply plopped onto your disk as regular files, organized in folders by date and data source.

**WIP Notice:** This project works as documented for the most part, but is still very young. Please consider this experimental until stable releases. The documentation needs a lot of work too, I know... so feel free to contribute!


## About

In general, Timeliner obtains _items_ from _data sources_ and stores them in a _timeline_.

- **Items** are anything that has content: text, image, video, etc. For example: photos, tweets, social media posts, even locations.
- **Data sources** are anything that can provide a list of items. For example: social media sites, online services, archive files, etc.
- **Timelines** are repositories that store the data. Typically, you will have one timeline that is your own, but timelines can support multiple people and multiple accounts per person if you desire to share it.

Technically speaking:

- An **Item** implements [this interface](https://pkg.go.dev/github.com/mholt/timeliner#Item) and provides access to its content and metadata.
- A **DataSource** is defined by [this struct](https://pkg.go.dev/github.com/mholt/timeliner#DataSource) which configures a [Client](https://pkg.go.dev/github.com/mholt/timeliner#Client) to access it (by its `NewClient` field). Clients are the types that do the actual work of listing of items.
- A **Timeline** is opened when being used. It consists of an underlying SQLite database and an adjacent data folder where larger/media items are stored as files. Timelines are essentially the folder that contains them. They are portable, so you can move them around and won't break things. However, don't change the contents of the folder directly! Don't add, remove, or modify items in the folder; you will break something. This does not mean timelines are read-only: they just have to be modified through the program in order to stay consistent.

Timeliner can pull data in from local or remote sources. It provides integrated support for OAuth2 and rate limiting where that is needed. It can also import data from local files. For example, some online services let you download all your data as an archive file. Timeliner can read those and index your data.

Timeliner data sources are strictly _read-only_ meaning that no write permissions are needed and Timeliner will never change or delete from the source.

## Features

- Supported data sources
	- [Facebook](https://github.com/mholt/timeliner/wiki/Data-Source:-Facebook)
	- [Google Location History](https://github.com/mholt/timeliner/wiki/Data-Source:-Google-Location-History)
	- [Google Photos](https://github.com/mholt/timeliner/wiki/Data-Source:-Google-Photos)
	- [Twitter](https://github.com/mholt/timeliner/wiki/Data-Source:-Twitter)
	- [Instagram](https://github.com/mholt/timeliner/wiki/Data-Source:-Instagram)
	- [SMS Backup & Restore](https://github.com/mholt/timeliner/wiki/Data-Source:-SMS-Backup-&-Restore)
	- **[Learn how to add more](https://github.com/mholt/timeliner/wiki/Writing-a-Data-Source)** - please contribute!
- Checkpointing (resume interrupted downloads)
- Pruning
- Integrity checks
- Deduplication
- Timeframing
- Differential reprocessing (only re-process items that have changed on the source)
- Construct graph-like relationships between items and people
- Memory-efficient for high-volume data processing
- Built-in rate limiting for API clients
- Built-in OAuth2 facilities for API clients
- Configurable data merging behavior for similar/identical items
- Ability to get and organize data from... almost anything, really, including export files

Some features are dependent upon the actual implementation of each data source. For example, differential reprocessing requires that the data source provide some sort of checksum or "ETag" for the item, but if that is not available, there's no way to know if an item has changed remotely without downloading the whole thing and reprocessing it.


## Install

**Minimum Go version required:** Go 1.13

Clone this repository, then from the project folder, run:

```
$ cd cmd/timeliner
$ go build
```

Then move the resulting executable into your PATH.



## Command line interface

This is a quick reference only. Be sure to read the tutorial below to learn how to use the program!

```
$ timeliner [<flags...>] <command> <args...>
```

Use `timeliner -h` to see available flags.

### Commands

- **`add-account`** adds a new account to the timeline and, if relevant, authenticates with the data source so that items can be obtained from an API. This only has to be done once per account per data source:
	```
	$ timeliner add-account <data_source>/<username>...
	```
	If the data source requires authentication (for example with OAuth), be sure the config file is properly created first.
- **`reauth`** re-authenticates with a data source. This is only necessary on some data sources that expire auth leases after some time:
	```
	$ timeliner reauth <data_source>/<username>...
	```
- **`import`** adds items from a local file:
	```
	$ timeliner import <filename> <data_source>/<username>
	```
- **`get-all`** adds items from the service's API.
	```
	$ timeliner get-all <data_source>/<username>...
	```
- **`get-latest`** adds only the latest items from the service's API (since the last checkpoint):
	```
	$ timeliner get-latest <data_source>/<username>...
	```

Flags can be used to constrain or customize the behavior of commands (`timeliner -h` to list flags).

See the [wiki page for your data sources](https://github.com/mholt/timeliner/wiki) to know how to use the various data sources.


## Tutorial

_After you've read this tutorial, [the Timeliner wiki](https://github.com/mholt/timeliner/wiki/) has all the information you'll need for using each data source._

These are the basic steps for getting set up:

1. Create a `timeliner.toml` config file (if any data sources require authentication)
2. Add your data source accounts
3. Fill your timeline

All items are associated with an account from whence they come. Even if a data source doesn't have the concept of accounts, Timeliner still has to think there is one.

Accounts are designated in the form `<data source ID>/<user ID>`, for example: `twitter/mholt6`. The data source ID is shown on each data source's [wiki page](https://github.com/mholt/timeliner/wiki/). With some data sources (like the Twitter API), the user ID matters; so where possible, give the actual username or email address you use with that service. For data sources that don't have the concept of accounts or a login, choose a user ID you will recognize such that the data source ID + user ID are unique.

If we want to use accounts that require OAuth2, we need to configure Timeliner with OAuth2 app credentials. You can learn which data sources need OAuth2 and what their configuration looks like by reading their [wiki page](https://github.com/mholt/timeliner/wiki/). By default, Timeliner will try to load `timeliner.toml` from the current directory, but you can use the `-config` flag to change that. Here's a sample `timeliner.toml` file for authenticating with Google:

```
[oauth2.providers.google]
client_id = "YOUR_APP_ID"
client_secret = "YOUR_APP_SECRET"
auth_url = "https://accounts.google.com/o/oauth2/auth"
token_url = "https://accounts.google.com/o/oauth2/token"
```

With that file in place, let's create an account to store our Google Photos:

```
$ timeliner add-account google_photos/you@gmail.com
```

This will open your browser window to authenticate with OAuth2.

You will notice that a folder called `timeliner_repo` was created in the current directory. This is your timeline. You can move it around if you want, and then use the `-repo` flag to work with that timeline.

Now let's get all our stuff from Google Photos. And I mean, _all_ of it. It's ours, after all:

```
$ timeliner get-all google_photos/you@gmail.com
```

(You can list multiple accounts on a single command, except `import` commands.)

This process can take weeks if you have a large library. Even if you have a fast Internet connection, the client is carefully rate-limited to be a good API citizen, so the process will be slow.

If you open your timeline folder in a file browser, you will see it start to fill up with your photos from Google Photos.

Data sources may create checkpoints as they go. If so, `get-all` or `get-latest` will automatically resume the last listing if it was interrupted, but only if the same command is repeated (you can't resume a `get-latest` with `get-all`, for example, or with different timeframe parameters). In the case of Google Photos, each page of API results is checkpointed. Checkpoints are not intended for long-term pauses. In other words, a resume should happen fairly shortly after being interrupted, and should be resumed using the same command as before. (A checkpoint will be automatically resumed only if the command parameters are identical.)

Item processing is idempotent, so as long as items have faithfully-unique IDs from their account, items that already exist in the timeline will be skipped and/or processed much faster.


### Constraining within a timeframe

You can use the `-start` and `-end` flags to specify either absolute dates within which to constrain data collection, or with [duration values](https://golang.org/pkg/time/#ParseDuration) to specify a date relative to the current timestamp. These flags appear before the subcommand.

To get all the items newer than a certain date:


```
$ timeliner -start=2019/07/1 get-all ...
```

This will get all items dated July 1, 2019 or newer.

To get all items older than certain date:

```
$ timeliner -end=2020/02/29 get-all ...
```

This processes all items before February 29, 2020.

To create a bounded window, use both:

```
$ timeliner -start=2019/07/01 -end=2020/02/29 get-all ...
```

Durations can be used for relative dates. To get all items up to 30 days old:

```
$ timeliner -end=-720h get-all ...
```

Notice how the duration value is negative; this is because you want the end date to be 720 hours (30 days) in the past, not in the future.


### Pulling the latest

Once your initial download completes, you can run Timeliner so that only the latest items are retrieved:

```
$ timeliner get-latest google_photos/you@gmail.com
```

This will get only the items timestamped newer than the newest item in your timeline (from the last successful run).

If `get-latest` is interrupted after adding some newer items to the timeline, the next run of `get-latest` will not stop at the first new item added last time; it is smart enough to know that it was interrupted and needs to keep getting items all the way until the beginning of the last _successful_ run, as long as the command's parameters are the same. For example, re-running the last command will automatically resume where it left off; but changing the `-end` flag, for example, won't be able to resume.

This subcommand supports the `-end` flag, but not the `-start` flag (since the start is determined from the last downloaded item). One thing I like to do is use `-end=-720h` with my Google Photos to only download the latest photos that are at least 30 days old. This gives me a month to delete unwanted/duplicate photos from my cloud library before I store them on my computer permanently.


### Duplicate items

Timeliner often encounters the same items multiple times. By default, it skips items with the same ID as one already stored in the timeline because it is faster and more efficient, but you can also configure it to "reprocess" or "merge" duplicate items. These two concepts are distinct and important.

**Reprocessing** is when Timeliner completely replaces an existing item with a new one.

**Merging** is when Timeliner combines a new item's data with an existing item.

Neither happen by default because they can be less efficient or cause undesired results. In other words: by default, Timeliner will only download and process and item once. This makes its `get-all`, `get-latest`, and `import` commands idempotent.

#### Reprocessing

Reprocessing replaces items with the same ID. This happens if one of the following conditions is met:

- You run with the `-integrity` flag which enables integrity checks, and an item's data file fails the integrity check. In that case, the item will be reprocessed to restore its correct data.

- The item has changed on the data source _and the data source indicates this change somehow_. However, very few (if any?) data sources actually provide a hash or ETag to help us compare whether a resource has changed.

- You run with the `-reprocess` flag. This does a "full reprocess" (or "forced reprocess") which indiscriminately reprocesses every item, just in case it changed. In other words, a forced reprocess will update your local copy with the source's latest for every item. This is often used because a data source might not provide enough information to automatically determine whether an item has changed. If you know you have changed your items on the data source, you could specify this flag to force Timeliner to update everything.


#### Merging

Merging combines two items without completely replacing the old item. Merges are additive: they'll never replace a field with a null value. By default, merges only add data that was missing and will not overwrite existing data (but this is configurable).

In theory, any two items can be merged, even if they don't have the same ID. Currently, the only way to trigger a merge is to enable "soft merging" which allows Timeliner to treat two items with different IDs as identical if ALL of these are true:

- They have the same account (same data source)
- They have the same timestamp
- They have either the same text data OR the same data file name

Merging can be enabled and customized with the `-merge` flag. This flag accepts a comma-separated list of merge options:

- `soft` (required): Enables soft merging. Currently, this is the only way to enable merging at all.
- `id`: Prefer new item's ID
- `text`: Prefer new item's text data
- `file`: Prefer new item's data file
- `meta`: Prefer new item's metadata

**Soft merging** simply updates the ID of either the existing, stored item or the new, incoming item to be the same as the other. (As with other fields, the ID of the existing item will be preferred by default, meaning the ID of the new item will be adjusted to match it.)

**Example:** I often use soft merging with Google Photos. Because the Google Photos API strips location data (grrr), I also use Google Takeout to import an archive of my photos. This adds the location data. However, although the archive has coordinate data, it does NOT have IDs like the Google Photos API provides. Thus, soft merging prevents a duplication of my photo library in my timeline.

To illustrate, I schedule this command to run regularly:

```
$ timeliner -merge=soft,id,meta -end=-720h get-latest google_photos/me
```

This uses the API to pull the latest photos up to 30 days old so I have time to delete unwanted photos from my library first. Notably, I enable soft merging and prefer the IDs and metadata given by the Google Photos API because they are richer and more precise.

Occasionally I will use Takeout to download an archive to add location data to my timeline, which I import like this:

```
$ timeliner -merge=soft import takeout.tgz google_photos/me
```

Note that soft merging is still enabled, but I always prefer existing data when doing this because all I want to do is fill in the missing location data.

This pattern takes advantage of soft merging and allows me to completely back up my Photos library locally, complete with location data, using both the API and Google Takeout.


### Pruning your timeline

Suppose you downloaded a bunch of photos with Timeliner that you later deleted from Google Photos. Timeliner can remove those items from your local timeline, too, to save disk space and keep things clean.

To schedule a prune, just run with the `-prune` flag: 

```
$ timeliner -prune get-all ...
```

However, this involves doing a complete listing of all the items. Pruning happens at the end. Any items not seen in the listing will be deleted. This also means that a full, uninterrupted listing is required, since resuming from a checkpoint yields an incomplete file listing. Pruning after a resumed listing will result in an error. (There's a TODO to improve this situation -- feel free to contribute! We just need to preserve the item listing along with the checkpoint.)

Beware! If your timeline has extra items added from auxillary sources (for example, using `import` with an archive file in addition to the regular API pulls), the prune operation may not see those extra items and thus delete them. Always back up your timeline before doing a prune.


### Reauthenticating with a data source

Some data sources (Facebook) expire tokens that don't have recent user interactions. Every 2-3 months, you may need to reauthenticate:

```
$ timeliner reauth facebook/you
```

See the [wiki](https://github.com/mholt/timeliner/wiki) for each data source to know if you need to reauthenticate and how to do so. Sometimes you have to go to the data source itself and authorize a reauthentication first.


### More information about each data source

Congratulations, you've [graduated to the wiki pages](https://github.com/mholt/timeliner/wiki) to learn more about how to set up and use each data source.



## Motivation and long-term vision

The motivation for this project is two-fold. Both press upon me with a sense of urgency, which is why I dedicated some nights and weekends to work on this.

1) Connecting with my family -- both living and deceased -- is important to me and my close relatives. But I wish we had more insights into the lives and values of those who came before us. What better time than right now to start collecting personal histories from all available sources and develop a rich timeline of our life for our family, and maybe even for our own reference or nostalgia.

2) Our lives are better-documented than any before us, but the documentation is more ephemeral than any before us, too. We lose control of our data by relying on centralized, proprietary cloud services which are useful today, and gone tomorrow. I wrote Timeliner because now is the time to liberate my data from corporations who don't own it, yet who have the only copy of it. This reality has made me feel uneasy for years, and it's not going away soon. Timeliner makes it bearable.

Imagine being able to pull up a single screen with your data from any and all of your online accounts and services -- while offline. And there you see so many aspects of your life at a glance: your photos and videos, social media posts, locations on a map and how you got there, emails and letters, documents, health and physical activities, and even your GitHub projects (if you're like me), for any given day. You can "zoom out" and get the big picture. Machine learning algorithms could suggest major clusters based on your content to summarize your days, months, or years, and from that, even recommend printing physical memorabilia. It's like a highly-detailed, automated journal, fully in your control, which you can add to in the app: augment it with your own thoughts like a regular journal.

Then cross-reference your own timeline with a global public timeline: see how locations you went to changed over time, or what major news events may have affected you, or what the political/social climate was like at the time.

Or translate the projection sideways, and instead of looking at time cross-sections, look at cross-sections of your timeline by media type: photos, posts, location, sentiment. Look at plots, charts, graphs, of your physical activity.

And all of this runs on your own computer: no one else has access to it, no one else owns it, but you.


## Viewing your Timeline

There is not yet a viewer for the timeline. For now, I've just been using [Table Plus](https://tableplus.io) to browse the SQLite database, and my file browser to look at the files in it. The important thing is that you have them, at least.

However, a viewer would be really cool. It's something I've been wanting to do but don't have time for right now. Contributions are welcomed along these lines, but this feature _must_ be thoroughly discussed before any pull requests will be accepted to implement a timeline viewer. Thanks!


## Notes

Yeah, I know this is very similar to what [Perkeep](https://perkeep.org/) does. Perkeep is a way cooler project in my opinion. However, Perkeep is more about storage and sync, whereas Timeliner is more focused on constructing relationships between items and projecting your digital life onto a single timeline. If Perkeep is my unified personal data storage, then Timeliner is my automatic journal. (Believe me, my heart sank after I realized that I was almost rewriting parts of Perkeep, until I decided that the two are different enough to warrant a separate project.)


## License

This project is licensed with AGPL. I chose this license because I do not want others to make proprietary software using this package. The point of this project is liberation of and control over one's own, personal data, and I want to ensure that this project won't be used in anything that would perpetuate the walled garden dilemma we already face today.

