Timeliner [![timeliner GoDoc](https://img.shields.io/badge/reference-godoc-blue.svg?style=flat-square)](https://godoc.org/github.com/mholt/timeliner) <!--[![Linux Build Status](https://img.shields.io/travis/mholt/archiver.svg?style=flat-square&label=linux+build)](https://travis-ci.org/mholt/archiver) [![Windows Build Status](https://img.shields.io/appveyor/ci/mholt/archiver.svg?style=flat-square&label=windows+build)](https://ci.appveyor.com/project/mholt/archiver)-->
=========

Timeliner is a personal data aggregation utility. It collects all your digital things from pretty much anywhere and stores them on your own computer, indexes them, and projects them onto a single, unified timeline.

The intended purpose of this tool is to help preserve personal and family history.

Things that are stored by Timeliner are easily accessible in a SQLite database or, for media items like photos and videos, are simply plopped onto your disk as regular files, organized in folders by date and data source.

**WIP Notice:** This project works as documented for the most part, but is still very young. Please consider this experimental until stable releases. The documentation needs a lot of work too, I know, so feel free to contribute.


## About

In general, Timeliner obtains _items_ from _data sources_ and stores them in a _timeline_.

- **Items** are anything that has content: text, image, video, etc. For example: photos, tweets, social media posts, even locations.
- **Data sources** are anything that can provide a list of items. For example: social media sites, online services, archive files, etc.
- **Timelines** are repositories that store the data. Typically, you will have one timeline that is your own, but timelines can support multiple people and multiple accounts per person if you desire to share it.

Technically speaking:

- An **Item** implements [this interface](https://godoc.org/github.com/mholt/timeliner#Item) and provides access to its content and metadata.
- A **DataSource** is defined by [this struct](https://godoc.org/github.com/mholt/timeliner#DataSource) and configures a Client to access it. Clients are the types that do the actual work of listing of items.
- A **Timeline** is opened when being used. It consists of an underlying SQLite database and an adjacent data folder where larger/media items are stored as files. Timelines are essentially the folder that contains them. They are portable, so you can move them around and won't break things. However, don't change the contents of the folder directly! Don't add, remove, or modify items in the folder; you will break something. This does not mean timelines are read-only: they just have to be modified through the program in order to stay consistent.

Timeliner can pull data in from local or remote sources. It provides integrated support for OAuth2 and rate limiting where that is needed. It can also import data from local files. For example, some online services let you download all your data as an archive file. Timeliner can read those and index your data.

Timeliner data sources are strictly _read-only_ meaning that no write permissions are needed and Timeliner will never change or delete the source.

## Features

- Supported data sources
	- [Facebook](https://github.com/mholt/timeliner/wiki/Data-Source:-Facebook)
	- [Google Location History](https://github.com/mholt/timeliner/wiki/Data-Source:-Google-Location-History)
	- [Google Photos](https://github.com/mholt/timeliner/wiki/Data-Source:-Google-Photos)
	- [Twitter](https://github.com/mholt/timeliner/wiki/Data-Source:-Twitter)
	- **[Learn how to add more](https://github.com/mholt/timeliner/wiki/Writing-a-Data-Source)** - we'd love your contribution!
- Checkpointing (resume interrupted downloads)
- Pruning
- Integrity checks
- Deduplication
- Differential reprocessing (only re-process items that have changed on the source)
- Construct graph-like relationships between items and people
- Memory-efficient for high-volume data processing
- Built-in rate limiting for API clients
- Built-in OAuth2 facilities for API clients
- Ability to get and organize data from... almost anything, really, including export files

Some features are dependent upon the actual implementation of each data source. For example, differential reprocessing requires that the data source provide some sort of checksum or "ETag" for the item, but if that is not available, there's no way to know if an item has changed remotely without downloading the whole thing and reprocessing it.


## Install

```
$ go get -u github.com/mholt/timeliner/cmd/timeliner
```

## Tutorial

_After you've read this tutorial, [the Timeliner wiki](https://github.com/mholt/timeliner/wiki/) has all the information you'll need for using each data source._

All items are associated with an account from whence they come. Even if a data source doesn't have the concept of accounts, Timeliner still has to think there is one.

Accounts are designated in the form `<data source ID>/<user ID>`, for example: `twitter/@mholt6`. The data source ID is shown on each data source's [wiki page](https://github.com/mholt/timeliner/wiki/). The user ID does not necessarily matter; just choose one that you will recognize, such that the data source ID + user ID are unique. Typically, a good user ID is your login username or email address.

If we want to use accounts that require OAuth2, we need to configure Timeliner with OAuth2 app credentials. You can learn which data sources need OAuth2 and what their configuration looks like by reading their [wiki page](https://github.com/mholt/timeliner/wiki/). By default, Timeliner will try to load `config.toml` from the current directory, but you can use the `-config` flag to change that. Here's a sample `config.toml` file for authenticating with Google:

```
[oauth2.providers.google]
client_id = "YOUR_APP_ID"
client_secret = "YOUR_APP_SECRET"
auth_url = "https://accounts.google.com/o/oauth2/auth"
token_url = "https://accounts.google.com/o/oauth2/token"
```

With that, let's create an account to store our Google Photos:

```
$ timeliner add-account google_photos/you@gmail.com
```

This will open your browser window to authenticate with OAuth2.

You will notice that a folder called `timeliner_repo` was created in the current directory. This is your timeline. You can move it around if you want, and then use the `-repo` flag to work with that timeline.

Now let's get all our stuff from Google Photos. And I mean, _all_ of it. It's ours, after all, not Google's:

```
$ timeliner get-all google_photos/you@gmail.com
```

(You can list any number of accounts on a single command; except for `import` commands.)

This process can take weeks if you have a large library. Even if you have a fast Internet connection, the client is carefully rate-limited to be a good API citizen, so the process will be slow.

If you open your timeline folder in a file browser, you will see it start to fill up with your photos from Google Photos.

Data sources may create checkpoints as they go. If so, `get-all` will automatically resume the last listing if it was interrupted. In the case of Google Photos, each page of API results is checkpointed. Checkpoints are not intended for long-term pauses. In other words, a resume should happen fairly shortly after being interrupted.

Item processing is idempotent, so as long as items have faithfully-unique IDs across each account, items that already exist in the timeline will be skipped and/or processed much faster.



### Pulling the latest

Once your initial download completes, you can run Timeliner so that only the latest items are retrieved:

```
$ timeliner get-latest google_photos/you@gmail.com
```

This will get only the items timestamped newer than the newest item in your timeline.


### Pruning your timeline

Suppose you downloaded a bunch of photos with Timeliner that you later deleted from Google Photos. Timeliner can remove those items from your local timeline, too, to save disk space and keep things clean.

However, this involves doing a complete listing of all the items. Pruning happens at the end. Any items not seen in the listing will be deleted. This also means that a full, uninterrupted listing is required, since resuming from a checkpoint yields an incomplete file listing. Pruning after a resumed listing will result in an error. (There's a TODO to improve this situation -- feel free to contribute! We just need to preserve the item listing along with the checkpoint.)


### More information about each data source

Congratulations, you've [graduated to the wiki pages](https://github.com/mholt/timeliner/wiki) to learn more about how to set up and use each data source.


## Viewing your Timeline

There is not yet a nice, all-in-one viewer for the timeline. I've just been using [Table Plus](https://tableplus.io) to browse the SQLite database, and my file browser to look at the files in it. The important thing is that you have them, at least.

However, a viewer would be really cool. It's something I've been wanting to do but don't have time for right now. Contributions are welcomed along these lines, but this feature _must_ be thoroughly discussed before any pull requests will be accepted to implement a timeline viewer. Thanks!



## License

This project is licensed with AGPL. I chose this license because I do not want others to make proprietary software using this package. The point of this project is liberation of and control over one's own, personal data, and I want to ensure that this project won't be used in anything that would perpetuate the walled garden dilemma we already face today.


## Notes

Yes, I know this is very similar to what [Perkeep](https://perkeep.org/) does. Perkeep is a way cooler project in my opinion. However, Perkeep is more about storage and sync, whereas Timeliner is more focused on constructing relationships between items and projecting your digital life onto a single timeline. If Perkeep is my unified personal data storage, then Timeliner is my automatic journal. (Believe me, my heart sank after I realized that I was almost rewriting parts of Perkeep, until I decided that the two are different enough to warrant a separate project.)

