package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// newFileCmd is the root for `any file <subcommand>` — the CLI face of
// the files v2 surface (docs/16-files.md). attach/download move raw
// bytes (the two non-JSON flows in the CLI); everything else prints
// the usual JSON.
func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "file",
		Aliases: []string{"files"},
		Short:   "attach, download and manage files bound to objects",
	}
	cmd.AddCommand(
		newFileAttachCmd(),
		newFileListCmd(),
		newFileGetCmd(),
		newFileDownloadCmd(),
		newFileStatusCmd(),
		newFileStatsCmd(),
		newFileSubscribeCmd(),
		newFilePinCmd(),
		newFileRetryCmd(),
		newFileOffloadCmd(),
		newFileQueryCmd(),
		newFileQuerySubscribeCmd(),
		newFileCacheCmd(),
	)
	return cmd
}

func newFileAttachCmd() *cobra.Command {
	var (
		name      string
		mimeType  string
		variant   string
		variantOf string
	)
	cmd := &cobra.Command{
		Use:   "attach <spaceId> <objectId> <path>",
		Short: "upload a file and bind it to an object (path - reads stdin)",
		Long: `Uploads the file at <path> (or stdin with -) as the raw request body of
POST /v1/spaces/:spaceId/objects/:objectId/files. Name defaults to the
file's basename, mime to the extension's type; both can be overridden.
Prints the FileInfo receipt — durable:false right after attach is
normal, backup runs in the background (watch with 'any file subscribe').`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[2]
			var in io.Reader
			if path == "-" {
				in = os.Stdin
			} else {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				in = f
				if name == "" {
					name = filepath.Base(path)
				}
				if mimeType == "" {
					mimeType = mime.TypeByExtension(filepath.Ext(path))
				}
			}
			cl := client.New(flags.Addr, 0) // uploads must not hit the request timeout
			out, err := cl.FileAttach(cmd.Context(), args[0], args[1], in, client.FileAttachOpts{
				Name:      name,
				Mime:      mimeType,
				Variant:   variant,
				VariantOf: variantOf,
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "user-facing file name (default: basename of <path>)")
	cmd.Flags().StringVar(&mimeType, "mime", "", "content type (default: from the file extension)")
	cmd.Flags().StringVar(&variant, "variant", "", "attach as this variant of an existing file (requires --variant-of)")
	cmd.Flags().StringVar(&variantOf, "variant-of", "", "fileId of the original this variant belongs to")
	return cmd
}

func newFileListCmd() *cobra.Command {
	var (
		objectId string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "list <spaceId>",
		Short: "list the space's files (typed member view)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.FileList(cmd.Context(), args[0], objectId, limit)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&objectId, "object", "", "restrict to one object's files")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap the result (0 = unlimited)")
	return cmd
}

func newFileGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <spaceId> <fileId>",
		Short: "print one file's info",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.FileGet(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newFileDownloadCmd() *cobra.Command {
	var (
		output  string
		variant string
	)
	cmd := &cobra.Command{
		Use:   "download <spaceId> <fileId>",
		Short: "download a file's content (stdout by default, -o for a file)",
		Long: `Streams GET /v1/spaces/:spaceId/files/:fileId/content. Without -o the
raw bytes go to stdout (pipe them); with -o PATH they are written to
PATH (PATH - is stdout too) and a small JSON receipt is printed.
Content not yet local streams in from the network on demand.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, 0) // downloads must not hit the request timeout
			res, err := cl.FileDownload(cmd.Context(), args[0], args[1], variant)
			if err != nil {
				return err
			}
			defer res.Body.Close()

			if output == "" || output == "-" {
				_, err := io.Copy(os.Stdout, res.Body)
				return err
			}
			f, err := os.Create(output)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, res.Body)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			return printJSON(map[string]any{
				"path":  output,
				"bytes": n,
				"mime":  res.Mime,
				"name":  res.Name,
			})
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write content to this path (default: stdout)")
	cmd.Flags().StringVar(&variant, "variant", "", "download this variant instead of the original")
	return cmd
}

func newFileStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <spaceId> <fileId>",
		Short: "print one file's durability status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.FileStatus(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newFileStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats <spaceId>",
		Short: "print the space's aggregate file durability counts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.FileStats(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newFileSubscribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscribe <spaceId>",
		Short: "stream file durability transitions (SSE; one JSON frame per line)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, 0) // timeout doesn't apply to streams
			enc := json.NewEncoder(os.Stdout)
			return cl.StreamFileStatusSubscribe(cmd.Context(), args[0], func(f client.SSEFrame) error {
				if f.Event == "" {
					return nil
				}
				return enc.Encode(struct {
					Event string          `json:"event"`
					Data  json.RawMessage `json:"data,omitempty"`
				}{Event: f.Event, Data: json.RawMessage(f.Data)})
			})
		},
	}
}

// newFilePinCmd / newFileRetryCmd / newFileOffloadCmd — the 204 verbs.
// Print nothing on success (same convention as `any space sync`).
func newFilePinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pin <spaceId> <fileId>",
		Short: "schedule a full background fetch into the local cache",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.FilePin(cmd.Context(), args[0], args[1])
		},
	}
}

func newFileRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry <spaceId> <fileId>",
		Short: "make the file's pending background work due immediately",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.FileRetry(cmd.Context(), args[0], args[1])
		},
	}
}

func newFileOffloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "offload <spaceId> <fileId>",
		Short: "drop the file's local bytes (refused while they are the only copy)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.FileOffload(cmd.Context(), args[0], args[1])
		},
	}
}

func newFileQueryCmd() *cobra.Command {
	var (
		filter     string
		sort       string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "query <spaceId> <objectId>",
		Short: "snapshot one object's file payload rows (cleartext fields)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildFilesQueryBody(filter, sort, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.FilesQuery(cmd.Context(), args[0], args[1], body)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addFilesQueryFlags(cmd, &filter, &sort, &limit, &offset, &includeTot)
	return cmd
}

func newFileQuerySubscribeCmd() *cobra.Command {
	var (
		filter     string
		sort       string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "query-subscribe <spaceId> <objectId>",
		Short: "live windowed view over one object's file payload rows (SSE)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildFilesQueryBody(filter, sort, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := client.New(flags.Addr, 0) // timeout doesn't apply to streams
			enc := json.NewEncoder(os.Stdout)
			return cl.StreamFilesQuerySubscribe(cmd.Context(), args[0], args[1], body, func(f client.SSEFrame) error {
				if f.Event == "" {
					return nil
				}
				return enc.Encode(struct {
					Event string          `json:"event"`
					Data  json.RawMessage `json:"data,omitempty"`
				}{Event: f.Event, Data: json.RawMessage(f.Data)})
			})
		},
	}
	addFilesQueryFlags(cmd, &filter, &sort, &limit, &offset, &includeTot)
	return cmd
}

func addFilesQueryFlags(cmd *cobra.Command, filter, sort *string, limit, offset *int, includeTot *bool) {
	cmd.Flags().StringVar(filter, "filter", "", "JSON filter object")
	cmd.Flags().StringVar(sort, "sort", "", "comma-separated sort keys (prefix '-' for descending)")
	cmd.Flags().IntVar(limit, "limit", 0, "window size")
	cmd.Flags().IntVar(offset, "offset", 0, "skip the first N records")
	cmd.Flags().BoolVar(includeTot, "total", false, "include the unbounded match count in the snapshot")
}

// buildFilesQueryBody assembles the query body for the files query
// endpoints — same fields as buildQueryBody minus objectId/dataset
// (both ride the path here).
func buildFilesQueryBody(filter, sort string, limit, offset int, includeTotal bool) ([]byte, error) {
	body := map[string]any{}
	if filter != "" {
		var f any
		if err := json.Unmarshal([]byte(filter), &f); err != nil {
			return nil, fmt.Errorf("--filter: %w", err)
		}
		body["filter"] = f
	}
	if sort != "" {
		var sorts []string
		for _, s := range splitCSV(sort) {
			if s != "" {
				sorts = append(sorts, s)
			}
		}
		if len(sorts) > 0 {
			body["sort"] = sorts
		}
	}
	if limit > 0 {
		body["limit"] = limit
	}
	if offset > 0 {
		body["offset"] = offset
	}
	if includeTotal {
		body["includeTotal"] = true
	}
	if len(body) == 0 {
		return nil, nil
	}
	return json.Marshal(body)
}

// newFileCacheCmd groups the account-wide cache controls.
func newFileCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "local file-cache controls (all spaces)",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "size",
			Short: "print the local bytes held by file content",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.FileCacheSize(cmd.Context())
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "free <bytes>",
			Short: "reclaim at least N bytes (LRU, safe-to-drop content only)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var n int64
				if _, err := fmt.Sscan(args[0], &n); err != nil || n <= 0 {
					return fmt.Errorf("bytes must be a positive integer")
				}
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.FileCacheFree(cmd.Context(), n)
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "sweep",
			Short: "run one file-cache safety pass",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.FileCacheSweep(cmd.Context())
			},
		},
	)
	return cmd
}
