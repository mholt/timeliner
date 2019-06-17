---
name: New data source request
about: Request a new data source
title: ''
labels: 'data source'
assignees: ''

---

<!--
This template is specifically for requesting the addition of a new data source (a way to add items to the timeline). Please answer all the questions as completely as possible. Put some effort into it since any implementation is going to require even more effort. If questions are not answered sufficiently, the issue may be closed.

PLEASE NOTE: This project is a community effort. We hope that after posting this issue, you will take the time to implement it and submit a pull request for everyone to use!
-->

## 1. What is the data source you want to add?
<!-- Please give the data source's name and website and explain why it would be useful to have its content on a personal timeline. -->




## 2. How are items obtained from the data source?
<!-- Is there a free API that allows us to get content from this source? If so, what authentication is required? Or do we have to manually import data from a file? Please describe the process in detail and link to documentation! -->




### 2a. If authentication is required, how does a user create or obtain credentials for Timeliner to access the data?
<!-- For example, APIs that use OAuth usually require creating an app or client with the service provider before their APIs can be accessed. What is that process? We will need to add this to the wiki for others to know how to get set up, so be clear and list the steps here. Check our project wiki first, because it might already be implemented (for example, Google OAuth is already in place.) -->




### 2b. If an API is available, what are its rate limits?
<!-- Please link to rate limit documentation as well. -->




### 2c. If a file is imported, how is the file obtained?
<!-- What is the process a user must go through to obtain the specific file that the data source is designed to import from? -->




### 2d. If a file is imported, how do we read the file?
<!-- Is the file a compressed archive? How do we get the items out? Is the content and metadata separate? Please link to any documentation or provide a sample file. -->




## 3. What constitutes an "item" from this data source?
<!-- An item is an entry on the timeline. Some data sources have multiple things that are "items" - for example: photos, blog posts, or text messages can all be items. An item must make sense to put on a timeline, and items must have unique IDs. -->



## 4. How can items from this data source be related?
<!-- Often, items form relationships with other items; for example, an item might be a reply to another item, or an item might contain another item. Think of relationships as uni-or-bi-directional arrows between items, with a label on the arrow. Relationships enrich the data obtained from this source. What kinds of useful relationships can be expressed from this data source? Do the relationships work both ways or just one way? Talk about this. -->




## 5. What constitutes a "collection" from this data source?
<!-- A collection is a group of items (like a photo album). Note that collections are different from item relationships. Some data sources don't have collections; please explain. -->




## 6. What might not be trivial, obvious, or straightforward when implementing this data source?
<!-- Most data sources have nuances or caveats, some of which might not be obvious. Please think hard about this and use your experience with this data source to think of things that need special consideration. For example, a data source might only allow the most recent items to be obtained; how could we overcome that, maybe via a data export? See our wiki for "Writing a Data Source" to get ideas about what might be tricky. Ask unanswered questions here, start a discussion. Data sources can't be implemented successfully until these details are figured out. -->




## Bonus: How do you like Timeliner? How much data are you preserving with it? Which existing data sources do you use?
<!-- I want to know! -->


