package tusgo

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/bdragon300/tusgo/checksum"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vitorsalgado/mocha/v3"
	"github.com/vitorsalgado/mocha/v3/expect"
	"github.com/vitorsalgado/mocha/v3/reply"
)

// TODO: set emptyHeaders
func tRequest(method, location string, emptyHeaders []string) *mocha.MockBuilder {
	b := mocha.Request().
		URL(expect.URLPath(location)).Method(method).
		Header("Location", expect.ToEqual(location)).
		Header("Tus-Resumable", expect.ToEqual("1.0.0"))
	for _, h := range emptyHeaders {
		b = b.Header(h, expect.ToBeEmpty())
	}
	return b
}

func tReply(startReply *reply.StdReply) *reply.StdReply {
	return startReply.Header("Tus-Resumable", "1.0.0")
}

var _ = Describe("Client", func() {
	var testClient *Client
	var testURL *url.URL
	var srvMock *mocha.Mocha

	BeforeEach(func() {
		srvMock = mocha.New(GinkgoT())
		srvMock.Start()
		testURL, _ = url.Parse(srvMock.URL())
		testClient = NewClient(http.DefaultClient, testURL)
		testClient.Capabilities = &ServerCapabilities{
			ProtocolVersions: []string{"1.0.0"},
		}
	})
	AfterEach(func() {
		if srvMock != nil {
			srvMock.AssertCalled(GinkgoT())
			Ω(srvMock.Close()).Should(Succeed())
		}
	})
	Context("NewClient", func() {
		It("should correct set initial values", func() {
			Ω(testClient.ProtocolVersion).Should(Equal("1.0.0"))
			Ω(testClient.Capabilities).Should(BeNil())
			Ω(testClient.GetRequest).ShouldNot(BeNil())
			Ω(testClient.BaseURL).Should(Equal(testURL))
			Ω(testClient.ctx).Should(BeNil())
		})
	})
	Context("WithContext", func() {
		It("should set context and return a copy of Client", func() {
			ctx := context.Background()
			res := testClient.WithContext(ctx)

			Ω(&res).ShouldNot(BeIdenticalTo(&testClient))
			Ω(res.ctx).Should(Equal(ctx))
		})
	})
	Context("tusRequest", func() {
		Context("happy path", func() {
			It("should make a request, return response", func() {
				srvMock.AddMocks(tRequest(http.MethodGet, "/foo", nil).Reply(tReply(reply.OK())))
				req, err := http.NewRequest(http.MethodGet, srvMock.URL()+"/foo", nil)
				Ω(err).Should(Succeed())

				Ω(testClient.tusRequest(context.Background(), req)).ShouldNot(BeNil())
			})
			When("OPTIONS request", func() {
				It("should not set Tus-Resumable header", func() {
					srvMock.AddMocks(mocha.Request().
						URL(expect.URLPath("/foo")).Method(http.MethodOptions).
						Header("Location", expect.ToEqual("/foo")).
						Header("Tus-Resumable", expect.ToBeEmpty()).
						Reply(tReply(reply.OK())),
					)
					req, err := http.NewRequest(http.MethodOptions, srvMock.URL()+"/foo", nil)
					Ω(err).Should(Succeed())

					Ω(testClient.tusRequest(context.Background(), req)).ShouldNot(BeNil())
				})
			})
			It("should use context", func() {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				req, err := http.NewRequest(http.MethodGet, srvMock.URL()+"/foo", nil)
				Ω(err).Should(Succeed())

				_, err = testClient.tusRequest(ctx, req)
				Ω(err).Should(MatchError(context.Canceled))
			})
		})
		Context("error path", func() {
			It("should process http 412 unknown versions", func() {
				srvMock.AddMocks(tRequest(http.MethodGet, "/foo", nil).
					Reply(reply.Status(http.StatusPreconditionFailed).
						Header("Tus-Version", "1.0.1,0.9.0")),
				)
				req, err := http.NewRequest(http.MethodGet, srvMock.URL()+"/foo", nil)
				Ω(err).Should(Succeed())

				_, err = testClient.tusRequest(context.Background(), req)
				Ω(err).Should(And(
					MatchError(ErrProtocol),
					MatchError(ContainSubstring("request protocol version '1.0.0', server supported versions: '1.0.1,0.9.0'")),
				))
			})
			When("request protocol version is not equal to response protocol version", func() {
				It("should return protocol error", func() {
					srvMock.AddMocks(tRequest(http.MethodGet, "/foo", nil).
						Reply(reply.OK().Header("Tus-Resumable", "0.9.0")),
					)
					req, err := http.NewRequest(http.MethodGet, srvMock.URL()+"/foo", nil)
					Ω(err).Should(Succeed())

					_, err = testClient.tusRequest(context.Background(), req)
					Ω(err).Should(And(
						MatchError(ErrProtocol),
						MatchError(ContainSubstring("server response protocol version '0.9.0', requested version '1.0.0'")),
					))
				})
			})
		})
	})
	Context("GetUpload", func() {
		Context("happy path", func() {
			When("ordinary upload", func() {
				It("should get upload info", func() {
					srvMock.AddMocks(tRequest(http.MethodHead, "/foo/bar", nil).
						Reply(tReply(reply.OK()).
							Header("Upload-Offset", "64")),
					)
					f := Upload{}

					Ω(testClient.GetUpload(&f, "/foo/bar")).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						Location:     "/foo/bar",
						RemoteOffset: 64,
					}))
				})
			})
			When("partial upload", func() {
				It("should get upload info", func() {
					srvMock.AddMocks(tRequest(http.MethodHead, "/foo/bar", nil).
						Reply(tReply(reply.OK()).
							Header("Upload-Concat", "partial").
							Header("Upload-Offset", "64")),
					)
					f := Upload{}

					Ω(testClient.GetUpload(&f, "/foo/bar")).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						Location:     "/foo/bar",
						RemoteOffset: 64,
						Partial:      true,
					}))
				})
			})
			When("final upload", func() {
				It("should get upload info", func() {
					srvMock.AddMocks(tRequest(http.MethodHead, "/foo/bar", nil).
						Reply(tReply(reply.OK()).
							Header("Upload-Concat", "final").
							Header("Upload-Offset", "64").
							Header("Upload-Length", "1024")),
					)
					f := Upload{}

					Ω(testClient.GetUpload(&f, "/foo/bar")).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						Location:     "/foo/bar",
						RemoteOffset: 64,
						Partial:      false,
						RemoteSize:   1024,
					}))
				})
				It("should get upload info without Upload-Offset header", func() {
					srvMock.AddMocks(tRequest(http.MethodHead, "/foo/bar", nil).
						Reply(tReply(reply.OK()).
							Header("Upload-Concat", "final").
							Header("Upload-Length", "1024")),
					)
					f := Upload{}

					Ω(testClient.GetUpload(&f, "/foo/bar")).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						Location:   "/foo/bar",
						Partial:    false,
						RemoteSize: 1024,
					}))
				})
			})
		})
		Context("error path", func() {
			When("f is nil", func() {
				It("should panic", func() {
					Ω(func() { _, _ = testClient.GetUpload(nil, "/foo/bar") }).Should(Panic())
				})
			})
			When("http error or unexpected code", func() {
				DescribeTable("should return error",
					func(status int, expectErr error) {
						srvMock.AddMocks(tRequest(http.MethodHead, "/foo/bar", nil).Reply(reply.Status(status)))
						f := Upload{}

						resp, err := testClient.GetUpload(&f, "/foo/bar")
						Ω(resp).ShouldNot(BeNil())
						Ω(err).Should(MatchError(expectErr))
						Ω(f).Should(Equal(Upload{}))
					},
					Entry("404", http.StatusNotFound, ErrUploadDoesNotExist),
					Entry("410", http.StatusGone, ErrUploadDoesNotExist),
					Entry("403", http.StatusForbidden, ErrUploadDoesNotExist),
					Entry("400", http.StatusBadRequest, ErrUnexpectedResponse),
					Entry("201", http.StatusCreated, ErrUnexpectedResponse),
				)
			})
			When("corrupted numeric header value", func() {
				DescribeTable("should return protocol error",
					func(header, value string) {
						srvMock.AddMocks(tRequest(http.MethodHead, "/foo/bar", nil).
							Reply(tReply(reply.OK()).
								Header(header, value)),
						)
						f := Upload{}

						resp, err := testClient.GetUpload(&f, "/foo/bar")
						Ω(resp).ShouldNot(BeNil())
						Ω(err).Should(MatchError(ErrProtocol))
						Ω(f).Should(Equal(Upload{}))
					},
					Entry("Upload-Offset", "Upload-Offset", "asdf"),
					Entry("Upload-Length", "Upload-Length", "asdf"),
				)
			})
		})
	})
	Context("CreateUpload", func() {
		Context("happy path", func() {
			BeforeEach(func() {
				testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "creation")
			})
			When("upload with size, without metadata", func() {
				It("should create upload", func() {
					eh := []string{"Upload-Metadata", "Upload-Concat", "Upload-Defer-Length"}
					srvMock.AddMocks(tRequest(http.MethodPost, "/", eh).
						Header("Content-Length", expect.ToEqual("0")).
						Header("Upload-Length", expect.ToEqual("1024")).
						Reply(tReply(reply.Created()).
							Header("Location", "/foo/bar")),
					)
					f := Upload{}

					Ω(testClient.CreateUpload(&f, 1024, false, nil)).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						RemoteSize: 1024,
						Location:   "/foo/bar",
					}))
				})
			})
			When("upload with size, with metadata", func() {
				It("should encode metadata and create upload", func() {
					eh := []string{"Upload-Concat", "Upload-Defer-Length"}
					md := map[string]string{
						"key1": "value1",
						"key2": "&^%$\"\t",
					}
					mdEncoded := "key1 dmFsdWUx,key2 Jl4lJCIJ"
					srvMock.AddMocks(tRequest(http.MethodPost, "/", eh).
						Header("Content-Length", expect.ToEqual("0")).
						Header("Upload-Length", expect.ToEqual("1024")).
						Header("Upload-Metadata", expect.ToEqual(mdEncoded)).
						Reply(tReply(reply.Created()).
							Header("Location", "/foo/bar")),
					)
					f := Upload{}

					Ω(testClient.CreateUpload(&f, 1024, false, md)).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						RemoteSize: 1024,
						Location:   "/foo/bar",
						Metadata:   md,
					}))
				})
			})
			When("partial upload with size, with metadata", func() {
				It("should encode metadata and create upload", func() {
					eh := []string{"Upload-Defer-Length"}
					md := map[string]string{
						"key1": "value1",
						"key2": "&^%$\"\t",
					}
					mdEncoded := "key1 dmFsdWUx,key2 Jl4lJCIJ"
					srvMock.AddMocks(tRequest(http.MethodPost, "/", eh).
						Header("Upload-Concat", expect.ToEqual("partial")).
						Header("Content-Length", expect.ToEqual("0")).
						Header("Upload-Length", expect.ToEqual("1024")).
						Header("Upload-Metadata", expect.ToEqual(mdEncoded)).
						Reply(tReply(reply.Created()).
							Header("Location", "/foo/bar")),
					)
					f := Upload{}

					Ω(testClient.CreateUpload(&f, 1024, true, md)).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						RemoteSize: 1024,
						Location:   "/foo/bar",
						Metadata:   md,
						Partial:    true,
					}))
				})
			})
			When("partial upload with defer size, with metadata", func() {
				It("should encode metadata and create upload", func() {
					testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "creation-defer-length")
					eh := []string{"Upload-Length"}
					md := map[string]string{
						"key1": "value1",
						"key2": "&^%$\"\t",
					}
					mdEncoded := "key1 dmFsdWUx,key2 Jl4lJCIJ"
					srvMock.AddMocks(tRequest(http.MethodPost, "/", eh).
						Header("Upload-Concat", expect.ToEqual("partial")).
						Header("Content-Length", expect.ToEqual("0")).
						Header("Upload-Defer-Length", expect.ToEqual("1")).
						Header("Upload-Metadata", expect.ToEqual(mdEncoded)).
						Reply(tReply(reply.Created()).
							Header("Location", "/foo/bar")),
					)
					f := Upload{}

					Ω(testClient.CreateUpload(&f, SizeUnknown, true, nil)).ShouldNot(BeNil())
					Ω(f).Should(Equal(Upload{
						RemoteSize: SizeUnknown,
						Location:   "/foo/bar",
						Metadata:   md,
						Partial:    true,
					}))
				})
			})
		})
		Context("error path", func() {
			When("f is nil", func() {
				It("should panic", func() {
					Ω(func() { _, _ = testClient.CreateUpload(nil, 1024, false, nil) }).Should(Panic())
				})
			})
			Specify("no creation extension", func() {
				f := Upload{}
				_, err := testClient.CreateUpload(&f, 1024, false, nil)
				Ω(err).Should(And(
					MatchError(ErrUnsupportedFeature), MatchError(ContainSubstring("server extension 'creation' is required")),
				))
			})
			Specify("no creation-defer-length extension and trying to create defer size upload", func() {
				testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "creation")
				f := Upload{}
				_, err := testClient.CreateUpload(&f, SizeUnknown, false, nil)
				Ω(err).Should(And(
					MatchError(ErrUnsupportedFeature), MatchError(ContainSubstring("server extension 'creation-defer-length' is required")),
				))
			})
			When("upload size is negative", func() {
				It("should panic", func() {
					testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "creation")
					f := Upload{}
					Ω(func() { _, _ = testClient.CreateUpload(&f, -2, false, nil) }).Should(Panic())
				})
			})
			Specify("metadata key contains a space", func() {
				testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "creation")
				md := map[string]string{
					"key 1": "value1",
					"key2":  "&^%$\"\t",
				}
				f := Upload{}
				_, err := testClient.CreateUpload(&f, 1024, false, md)
				Ω(err).Should(MatchError(ContainSubstring("key 'key 1' contains spaces")))
			})
			When("http error or unexpected code", func() {
				DescribeTable("should return error",
					func(status int, expectErr error) {
						testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "creation")
						srvMock.AddMocks(tRequest(http.MethodPost, "/foo/bar", nil).Reply(reply.Status(status)))
						f := Upload{}

						resp, err := testClient.CreateUpload(&f, 1024, false, nil)
						Ω(resp).ShouldNot(BeNil())
						Ω(err).Should(MatchError(expectErr))
						Ω(f).Should(Equal(Upload{RemoteSize: 1024}))
					},
					Entry("413", http.StatusRequestEntityTooLarge, ErrUploadTooLarge),
					Entry("404", http.StatusNotFound, ErrUnexpectedResponse),
					Entry("410", http.StatusGone, ErrUnexpectedResponse),
					Entry("403", http.StatusForbidden, ErrUnexpectedResponse),
					Entry("400", http.StatusBadRequest, ErrUnexpectedResponse),
					Entry("200", http.StatusOK, ErrUnexpectedResponse),
				)
			})
		})
	})
	PContext("CreateUploadWithData")
	Context("DeleteUpload", func() {
		Context("happy path", func() {
			BeforeEach(func() {
				testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "termination")
			})
			Specify("make a request", func() {
				srvMock.AddMocks(
					tRequest(http.MethodDelete, "/foo/bar", nil).
						Header("Content-Length", expect.ToEqual("0")).
						Reply(tReply(reply.NoContent())))
				f := Upload{Location: "/foo/bar"}
				Ω(testClient.DeleteUpload(&f)).ShouldNot(BeNil())
				Ω(f).Should(Equal(Upload{Location: "/foo/bar"}))
			})
		})
		Context("error path", func() {
			When("f is nil", func() {
				It("should panic", func() {
					Ω(func() { _, _ = testClient.DeleteUpload(nil) }).Should(Panic())
				})
			})
			Specify("no termination extension", func() {
				f := Upload{Location: "/foo/bar"}
				_, err := testClient.DeleteUpload(&f)
				Ω(err).Should(And(
					MatchError(ErrUnsupportedFeature), MatchError(ContainSubstring("server extension 'termination' is required")),
				))
			})
			When("http error or unexpected code", func() {
				DescribeTable("should return error",
					func(status int, expectErr error) {
						testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "termination")
						srvMock.AddMocks(tRequest(http.MethodDelete, "/foo/bar", nil).Reply(reply.Status(status)))
						f := Upload{Location: "/foo/bar"}

						resp, err := testClient.DeleteUpload(&f)
						Ω(resp).ShouldNot(BeNil())
						Ω(err).Should(MatchError(expectErr))
						Ω(f).Should(Equal(Upload{Location: "/foo/bar"}))
					},
					Entry("413", http.StatusRequestEntityTooLarge, ErrUploadTooLarge),
					Entry("404", http.StatusNotFound, ErrUploadDoesNotExist),
					Entry("410", http.StatusGone, ErrUploadDoesNotExist),
					Entry("403", http.StatusForbidden, ErrUploadDoesNotExist),
					Entry("400", http.StatusBadRequest, ErrUnexpectedResponse),
					Entry("200", http.StatusOK, ErrUnexpectedResponse),
				)
			})
		})
	})
	Context("ConcatenateUploads", func() {
		Context("happy path", func() {
			BeforeEach(func() {
				testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "concatenation")
			})
			When("send several uploads, no metadata", func() {
				It("should make a request", func() {
					eh := []string{"Upload-Length"}
					srvMock.AddMocks(tRequest(http.MethodPost, "/", eh).
						Header("Upload-Concat", expect.ToEqual("final")).
						Reply(tReply(reply.Created()).Header("Location", "/foo/bar/baz")),
					)
					f1 := Upload{Location: "/foo/bar", RemoteSize: 256, RemoteOffset: 256, Partial: true}
					f2 := Upload{Location: "/foo/baz", RemoteSize: 512, RemoteOffset: 512, Partial: true}
					f := Upload{}

					Ω(testClient.ConcatenateUploads(&f, []Upload{f1, f2}, nil)).Should(Succeed())
					Ω(f).Should(Equal(Upload{
						Location: "/foo/bar/baz",
						Partial:  false,
					}))
				})
			})
			When("send several uploads, with metadata", func() {
				It("should make a request", func() {
					eh := []string{"Upload-Length"}
					md := map[string]string{
						"key1": "value1",
						"key2": "&^%$\"\t",
					}
					mdEncoded := "key1 dmFsdWUx,key2 Jl4lJCIJ"
					srvMock.AddMocks(tRequest(http.MethodPost, "/", eh).
						Header("Upload-Concat", expect.ToEqual("final;/foo/bar /foo/baz")).
						Header("Upload-Metadata", expect.ToEqual(mdEncoded)).
						Reply(tReply(reply.Created()).Header("Location", "/foo/bar/baz")),
					)
					f1 := Upload{Location: "/foo/bar", RemoteSize: 256, RemoteOffset: 256, Partial: true}
					f2 := Upload{Location: "/foo/baz", RemoteSize: 512, RemoteOffset: 512, Partial: true}
					f := Upload{}

					Ω(testClient.ConcatenateUploads(&f, []Upload{f1, f2}, md)).Should(Succeed())
					Ω(f).Should(Equal(Upload{
						Location: "/foo/bar/baz",
						Partial:  false,
						Metadata: md,
					}))
				})
			})
		})
		Context("error path", func() {
			When("f is nil", func() {
				It("should panic", func() {
					testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "concatenation")
					f1 := Upload{Location: "/foo/bar", RemoteSize: 256, RemoteOffset: 256, Partial: true}
					f2 := Upload{Location: "/foo/baz", RemoteSize: 512, RemoteOffset: 512, Partial: true}
					Ω(func() { _, _ = testClient.ConcatenateUploads(nil, []Upload{f1, f2}, nil) }).Should(Panic())
				})
			})
			When("uploads list is empty", func() {
				It("should panic", func() {
					testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "concatenation")
					f := Upload{}
					Ω(func() { _, _ = testClient.ConcatenateUploads(&f, nil, nil) }).Should(Panic())
					Ω(f).Should(Equal(Upload{}))
				})
			})
			When("no concatenation extension", func() {
				It("should return error", func() {
					f1 := Upload{Location: "/foo/bar", RemoteSize: 256, RemoteOffset: 256, Partial: true}
					f2 := Upload{Location: "/foo/baz", RemoteSize: 512, RemoteOffset: 512, Partial: true}
					f := Upload{}
					Ω(testClient.ConcatenateUploads(&f, []Upload{f1, f2}, nil)).Should(MatchError(ContainSubstring("server extension 'concatenation' is required")))
					Ω(f).Should(Equal(Upload{}))
				})
			})
			When("some uploads are not partial", func() {
				It("should return error", func() {
					testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "concatenation")
					f1 := Upload{Location: "/foo/bar", RemoteSize: 256, RemoteOffset: 256, Partial: true}
					f2 := Upload{Location: "/foo/baz", RemoteSize: 512, RemoteOffset: 512, Partial: false}
					f3 := Upload{Location: "/foo/baa", RemoteSize: 512, RemoteOffset: 512, Partial: true}
					f := Upload{}
					Ω(testClient.ConcatenateUploads(&f, []Upload{f1, f2, f3}, nil)).Should(MatchError(ContainSubstring("upload '/foo/baz' is not partial")))
					Ω(f).Should(Equal(Upload{}))
				})
			})
			When("http error or unexpected code", func() {
				DescribeTable("should return error",
					func(status int, expectErr error) {
						testClient.Capabilities.Extensions = append(testClient.Capabilities.Extensions, "concatenation")
						srvMock.AddMocks(tRequest(http.MethodPost, "/", nil).Reply(reply.Status(status)))
						f1 := Upload{Location: "/foo/bar", RemoteSize: 256, RemoteOffset: 256, Partial: true}
						f2 := Upload{Location: "/foo/baz", RemoteSize: 512, RemoteOffset: 512, Partial: true}
						f := Upload{}

						resp, err := testClient.ConcatenateUploads(&f, []Upload{f1, f2}, nil)
						Ω(resp).ShouldNot(BeNil())
						Ω(err).Should(MatchError(expectErr))
						Ω(f).Should(Equal(Upload{}))
					},
					Entry("404", http.StatusNotFound, ErrUploadDoesNotExist),
					Entry("410", http.StatusGone, ErrUploadDoesNotExist),
					Entry("403", http.StatusForbidden, ErrUnexpectedResponse),
					Entry("400", http.StatusBadRequest, ErrUnexpectedResponse),
					Entry("200", http.StatusOK, ErrUnexpectedResponse),
				)
			})
		})
	})
	PContext("ConcatenateStreams")
	Context("UpdateCapabilities", func() {
		Context("happy path", func() {
			DescribeTable("should fill client capabilities",
				func(status int) {
					srvMock.AddMocks(
						mocha.Request().URL(expect.URLPath("/")).Method(http.MethodOptions).
							Reply(tReply(reply.Status(status)).
								Header("Tus-Version", "1.0.0,0.2.2,0.2.1").
								Header("Tus-Max-Size", "1073741824").
								Header("Tus-Extension", "creation,expiration,checksum").
								Header("Tus-Checksum-Algorithm", "sha1,md5")),
					)
					Ω(testClient.UpdateCapabilities()).ShouldNot(BeNil())
					Ω(testClient.Capabilities).Should(Equal(ServerCapabilities{
						Extensions:         []string{"creation", "expiration", "checksum"},
						MaxSize:            1073741824,
						ProtocolVersions:   []string{"1.0.0", "0.2.2", "0.2.1"},
						ChecksumAlgorithms: []checksum.Algorithm{checksum.SHA1, checksum.MD5},
					}))
				},
				Entry("200", http.StatusOK),
				Entry("204", http.StatusNoContent),
			)
		})
		Context("error path", func() {
			When("corrupted number in Tus-Max-Size", func() {
				It("should return error", func() {
					srvMock.AddMocks(
						mocha.Request().URL(expect.URLPath("/")).Method(http.MethodOptions).
							Reply(tReply(reply.OK()).
								Header("Tus-Version", "1.0.0,0.2.2,0.2.1").
								Header("Tus-Max-Size", "fdsa107374182dw4").
								Header("Tus-Extension", "creation,expiration,checksum").
								Header("Tus-Checksum-Algorithm", "sha1,md5")),
					)
					resp, err := testClient.UpdateCapabilities()
					Ω(resp).ShouldNot(BeNil())
					Ω(err).Should(MatchError(ContainSubstring("cannot parse Tus-Max-Size integer value 'fdsa107374182dw4'")))
				})
			})
			When("http error or unexpected code", func() {
				DescribeTable("should return error",
					func(status int, expectErr error) {
						srvMock.AddMocks(tRequest(http.MethodOptions, "/", nil).Reply(reply.Status(status)))

						resp, err := testClient.UpdateCapabilities()
						Ω(resp).ShouldNot(BeNil())
						Ω(err).Should(MatchError(expectErr))
					},
					Entry("404", http.StatusNotFound, ErrUnexpectedResponse),
					Entry("410", http.StatusGone, ErrUnexpectedResponse),
					Entry("403", http.StatusForbidden, ErrUnexpectedResponse),
					Entry("400", http.StatusBadRequest, ErrUnexpectedResponse),
					Entry("201", http.StatusCreated, ErrUnexpectedResponse),
				)
			})
		})
	})
	Context("ensureExtension", func() {
		When("extension exists", func() {
			When("capabilities are empty", func() {
				It("should request from server and return no error", func() {
					testClient.Capabilities = nil
					srvMock.AddMocks(
						mocha.Request().URL(expect.URLPath("/")).Method(http.MethodOptions).
							Reply(tReply(reply.OK()).
								Header("Tus-Version", "1.0.0,0.2.2,0.2.1").
								Header("Tus-Max-Size", "1073741824").
								Header("Tus-Extension", "creation,expiration,checksum").
								Header("Tus-Checksum-Algorithm", "sha1,md5")),
					)
					Ω(testClient.ensureExtension("creation")).Should(Succeed())
				})
			})
			When("capabilities are not empty", func() {
				It("should use cache and return no error", func() {
					testClient.Capabilities.Extensions = []string{"creation", "expiration"}
					Ω(testClient.ensureExtension("creation")).Should(Succeed())
				})
			})
		})
		When("no such extension", func() {
			It("should return error", func() {
				Ω(testClient.ensureExtension("creation")).Should(MatchError(ErrUnsupportedFeature))
			})
		})
	})
})

func ExampleClient_CreateUpload() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	u := Upload{}
	// Create an upload with size 1024 bytes
	if _, err = cl.CreateUpload(&u, 1024, false, nil); err != nil {
		panic(err)
	}
	fmt.Printf("Location: %s", u.Location)
}

func ExampleClient_ConcatenateUploads() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	wg := &sync.WaitGroup{}
	writeStream := func(s *UploadStream, size int64) {
		src := io.LimitReader(rand.New(rand.NewSource(time.Now().UnixNano())), size)
		if _, err := io.Copy(s, src); err != nil {
			panic(err)
		}
		fmt.Println("Copying upload completed")
		wg.Done()
	}
	wg.Add(2)

	// Create the 1st partial upload with size 1024 bytes
	u1 := Upload{}
	if _, err = cl.CreateUpload(&u1, 1024, true, nil); err != nil {
		panic(err)
	}
	go writeStream(NewUploadStream(cl, &u1), 1024)

	// Create the 2nd partial upload with size 512 bytes
	u2 := Upload{}
	if _, err = cl.CreateUpload(&u2, 512, true, nil); err != nil {
		panic(err)
	}
	go writeStream(NewUploadStream(cl, &u1), 512)

	wg.Wait()
	// Concatenate partial uploads into a final upload
	final := Upload{}
	if _, err = cl.ConcatenateUploads(&final, []Upload{u1, u2}, nil); err != nil {
		panic(err)
	}

	fmt.Printf("Location: %s", final.Location)

	// Get file info
	u := Upload{RemoteOffset: OffsetUnknown}
	for {
		if _, err = cl.GetUpload(&u, final.Location); err != nil {
			panic(err)
		}
		// When concatenation still in progress the offset can be either OffsetUnknown or a value less than size
		// depending on server implementation
		if u.RemoteOffset != OffsetUnknown && u.RemoteOffset == u.RemoteSize {
			break
		}
		fmt.Println("Waiting concatenation to be finished")
		time.Sleep(2 * time.Second)
	}

	fmt.Printf("Offset: %d, Size: %d", u.RemoteOffset, u.RemoteSize)
}
