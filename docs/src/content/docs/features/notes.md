---
title: Notes and whiteboard photos
description: 'Markdown notes with images on a training, and whiteboard photos written out by Claude.'
---

Each [training](/eumetria/features/trainings/) can have a note: the WOD from the whiteboard, how it went, anything else. Write it in a workout's window or in the Training form.

## Writing

Notes are Markdown: headings, bold, italic, lists, inline code, code blocks between ` ``` ` lines, and `http(s)` links. No HTML. **Preview** shows how it will look.

- **Images:** paste one with Ctrl+V or use **Add image**. It's uploaded at once, stripped of its metadata and embedded where the cursor is; images from other sites aren't shown.
- An image no note embeds is deleted after a day and left out of backups. Each user can have at most 20 waiting and upload 30 an hour; the folder takes at most 2 GB.
- A long note shows its start, with **Show more**.
- Text being written survives closing the window, until it's saved or cancelled.

## Whiteboard photos

A photo in a note, e.g. of the gym's whiteboard, can be written out as text: under the note, each photo has **Read the whiteboard**. Claude writes the workout as Markdown (parts as headings, movements with reps, loads and distances as written, unclear bits marked *(unreadable)*). The text goes where the photo was, and the photo moves to the end of the note.

- Nothing is saved until you save the note, so check the text first.
- Afterwards search finds it ("alternating DB snatch @ 15 kg"), and so do the share picture and the export.
- It needs Claude: **Settings → AI → Whiteboard photos** picks the API or Claude Code (Claude Code only for your own account), and optionally its own model. See [Claude](/eumetria/connect/claude/).
- Only people who may write notes can use it, on photos they uploaded or that the note already holds; 30 photos an hour each.

On an installed phone app you can share a photo from the camera or gallery straight to Eumetria; see [On your phone](/eumetria/features/phone/#share-a-photo).

## Where notes show

- In the workout's window, the activity's history and the search.
- In the share window: **Include note** makes a second picture with the whole note; see [Export, report and sharing](/eumetria/features/export-share/#share-pictures).
- In the export.

Notes and their images are backed up (`app.db` and `note-images/`). A note whose workouts have gone from Google stays until an admin deletes it under **Settings → Users & Roles → Notes**.

`view.notes` reads notes; `edit.notes` writes them (with a second factor).
