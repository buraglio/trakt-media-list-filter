# Trakt List greater and filter

This can be used with the trakt API to create lists of things based on filters. 

## Usage

Create a config.json file that looks like this: 

```
{
  "CLIENT_ID": "YOUR_CLIENT_ID",
  "CLIENT_SECRET": "YOUR_CLIENT_SECRET"
}
```
## Build it

```
go mod init trakt-media-list-filter
go mod tidy
go build -o trakt-media-list-filter

```


## Use it to build lists 

### Interactive person search, print matching movies
`./trakt-media-list-filter -n "Tura Satana"

### Create a list with only TV items
`./trakt-media-list-filter -n "Kevin Smith" -l "KevinSmithTV" --tv-only`

### Create a list with only movies
`./trakt-media-list-filter --trakt_id 138 --movies-only -l "NolanMovies"`


