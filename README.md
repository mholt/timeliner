Timeliner [![timeliner GoDoc](https://img.shields.io/badge/reference-godoc-blue.svg?style=flat-square)](https://godoc.org/github.com/mholt/timeliner) <!--[![Linux Build Status](https://img.shields.io/travis/mholt/archiver.svg?style=flat-square&label=linux+build)](https://travis-ci.org/mholt/archiver) [![Windows Build Status](https://img.shields.io/appveyor/ci/mholt/archiver.svg?style=flat-square&label=windows+build)](https://ci.appveyor.com/project/mholt/archiver)-->
=========

Timeliner is a personal data aggregation utility. It collects all your digital things from pretty much anywhere and stores them on your own computer, indexes them, and projects them onto a single, unified timeline.

The intended purpose of this tool is to help preserve personal and family history.

Things that are stored by Timeliner are easily accessible in a SQLite database or, for media items like photos and videos, are simply plopped onto your disk as regular files, organized in folders by date and data source.

**WORK IN PROGRESS NOTICE:** This project works as documented for the most part, but is still very young. Please contribute bug fixes, and note the TODOs in the code!


## About

In general, Timeliner obtains _items_ from _data sources_ and stores them in a _timeline_.

- **Items** are anything that has content: text, image, video, etc. For example: photos, tweets, social media posts, even locations.
- **Data sources** are anything that can provide a list of items. For example: social media sites, online services, archive files, etc.
- **Timelines** are repositories that store the data. Typically, you will have one timeline that is your own, but timelines can support multiple people and multiple accounts per person if you desire to share it.

Technically speaking:

- An **Item** implements this interface (TODO: godoc link) and provides access to its content and metadata.
- A **DataSource** is defined by this struct (TODO: godoc link) and configures a Client to access it. Clients are the types that actually do the listing of items.
- A **Timeline** is opened when being used. It consists of an underlying SQLite database and an adjacent data folder where larger/media items are stored as files. Timelines are essentially the folder that contains them. They are portable, so you can move them around and won't break things. However, don't change the contents of the folder directly! Don't add, remove, or modify items in the folder; you will break something. This does not mean timelines are read-only: they just have to be modified through the program in order to stay consistent.


## Supported Data Sources

Data sources along with their unique ID:

- Facebook (`facebook`)
- Google Location History (`google_location`)
- Google Photos (`google_photos`)
- Twitter (`twitter`)

With the possibility to add many more. Please contribute!


## Install

```
$ go get -u github.com/mholt/timeliner/cmd/timeliner
```

## Tutorial

All items are associated with an account from whence they come. Even if a data source doesn't have "accounts" strictly speaking, we still need to pretend they exist to keep things uniform.

Accounts are designated in the form `<data source ID>/<user ID>`. The user ID does not necessarily matter; just choose one that you will recognize, such that the data source ID + user ID are unique. Typically it is your login username or email address.

If we want to use accounts that require OAuth2, we need to configure Timeliner with OAuth2 app credentials. By default, Timeliner will try to load `config.toml` in the current directory, but you can use the `-config` flag to change that. Here's a sample `config.toml` file for authenticating with Google:

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

This process can take weeks if you have a large librarry. Even if you have a fast Internet connection, the client is carefully rate-limited to be a good API citizen, so the process will be slow.

If you open your timeline folder in a file browser, you will see it start to fill up with your photos from Google Photos.

Data sources may create checkpoints as they go, so if a listing is resumable, `get-all` will automatically resume the last download if it did not complete. In the case of Google Photos, each page of API results is checkpointed. Checkpoints are not intended for long-term pauses. In other words, a resume should happen fairly shortly after being interrupted.

Item processing is idempotent, so as long as items have faithfully-unique IDs across each account, items that already exist in the timeline will be skipped and/or processed much faster.


### Pulling the latest

Once your initial download completes, you can run Timeliner so that only the latest items are retrieved:

```
$ timeliner get-latest google_photos/you@gmail.com
```

This will get only the items timestamped newer than the newest item in your timeline.


### Pruning your timeline

Suppose you downloaded a bunch of photos with Timeliner that you later deleted from Google Photos. Timeliner can remove those items from your own timeline, too, to save disk space and keep things clean.

However, this involves doing a complete listing of all the items. Pruning happens at the end. Any items not seen in the listing will be deleted. This also means that a full, uninterrupted listing is required, since resuming from a checkpoint yields an incomplete file listing. Pruning after a resumed listing will result in an error. (There's a TODO to improve this situation -- feel free to contribute! We just need to preserve the item listing along with the checkpoint.)


## Viewing your Timeline

There is not currently a nice, all-in-one viewer for the timeline. I've just been using [Table Plus](https://tableplus.io) to browse the SQLite database, and my file browser to look at the files that are dumped. The important thing is that you have them.

However, a viewer would be really cool. Contributions are welcomed along these lines, but this feature _must_ be thoroughly discussed before any pull requests will be accepted to implement a timeline viewer. Thanks!



## License

This project is licensed with AGPL. I chose this license because although I do want this software to be used very liberally, I do not want others to make proprietary software using this package.

The point of this project is liberation of and control over one's own, personal data, and I want to ensure that this project won't be used in anything to perpetuate the walled garden situation we already face today.


## Notes

Yes, I know this is very similar to what [Perkeep](https://perkeep.org/) does. Perkeep is a way cooler project in my opinion. However, Perkeep is more about storage and sync, whereas Timeliner is more focused on constructing relationships between items and projecting your digital life onto a single timeline. If Perkeep is my unified personal data storage, then Timeliner is my automatic journal. (But yes, I did have a slight headache after I realized that I was almost rewriting parts of Perkeep, until I decided that the two are different enough to warrant a separate project.)

