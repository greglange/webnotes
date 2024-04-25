# Webnotes

Webnotes is bookmarks plus notes.

## Project status.

This project is under initial development.

- anything might change
- there are bugs
- no tests
- only verfied to work on Ubuntu
- sparse documentation

## Quick start usage.

Make a directory to hold your webnotes.

Change dir into your directory.

Run this command to create your first webnote:

`webnotes --add --vurl https://en.wikipedia.org/wiki/L._L._Zamenhof --p --title --vtags esperanto --out_file Languages.wn`

Edit `Languages.wn` to have the notes you want.

Run this command to build the index for your webnotes:

`webnotes --index`

Run this command to run a web server for your webnotes:

`webnotes --http`

Go to this url in your web browser to view your webnotes:

[http://localhost:8080/](http://localhost:8080)

## Version control system.

Webnotes is meant to be used with a version control system like `git`.

Some operations that modify webnotes files can fail in the middle and data can be lost.

Commit your files before modifying them with the `webnotes` command.

Verify the `webnotes` command modfied your files as expected.

There is no need to keep the webnotes index directory `wn_index` under version control.

## Main functions of webnotes command.

## File selection flags.

## Webnote selection flags.

## Value flags.

## File format.

## Example usage.
