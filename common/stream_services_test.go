package common

import (
	"fmt"
	"testing"

	"github.com/dominant-strategies/go-quai/common/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

const (
	// message length: 12
	shortMessage = "test message"
	shortLength  = 12
	// message length: 260
	longMessage = "the quick brown fox jumps over the lazy dog multiple times to make this message longer and longer and longer and longer and longer and longer and longer and longer and longer and longer and longer and longer and longer until it is more than 256 characters long"
	longLength  = 260
)

func TestWriteMessageToStream(t *testing.T) {
	t.Skip("Todo: fix failing test")
	tests := []struct {
		name      string
		message   []byte
		wantErr   bool
		setupMock func(*mocks.MockStream)
	}{
		{
			name:    "successful write with short message",
			message: []byte(shortMessage),
			wantErr: false,
			setupMock: func(m *mocks.MockStream) {
				m.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil)
				gomock.InOrder(
					// first expect the length of the message to be written
					m.EXPECT().Write(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, []byte{0, 0, 0, 12}, b)
						return 4, nil
					}),
					// then expect the message itself to be written
					m.EXPECT().Write(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, []byte(shortMessage), b)
						return shortLength, nil
					}),
				)
			},
		},
		{
			name:    "successful write with long message",
			message: []byte(longMessage),
			wantErr: false,
			setupMock: func(m *mocks.MockStream) {
				m.EXPECT().SetWriteDeadline(gomock.Any()).Return(nil)
				gomock.InOrder(
					// first expect the length of the message to be written
					m.EXPECT().Write(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, []byte{0, 0, 1, 4}, b) // 260 = 0x0104
						return 4, nil
					}),
					// then expect the message itself to be written
					m.EXPECT().Write(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, []byte(longMessage), b)
						return longLength, nil
					}),
				)
			},
		},
		{
			name:    "error on write deadline",
			message: []byte(shortMessage),
			wantErr: true,
			setupMock: func(m *mocks.MockStream) {
				m.EXPECT().SetWriteDeadline(gomock.Any()).Return(fmt.Errorf("error"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStream := mocks.NewMockStream(ctrl)
			tt.setupMock(mockStream)

			err := WriteMessageToStream(mockStream, tt.message)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReadMessageFromStream(t *testing.T) {
	t.Skip("Todo: fix failing test")
	tests := []struct {
		name      string
		setupMock func(*mocks.MockStream)
		want      []byte
		wantErr   bool
	}{
		{
			name: "successful read with short message",
			setupMock: func(m *mocks.MockStream) {
				m.EXPECT().SetReadDeadline(gomock.Any()).Return(nil)
				gomock.InOrder(
					// assert that the length of the message is read first
					m.EXPECT().Read(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, len(b), 4)
						// return 4 bytes that represent the length of the message
						copy(b, []byte{0, 0, 0, 12})
						return 4, nil
					}),
					// then assert that the message itself is read
					m.EXPECT().Read(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, len(b), 12)
						copy(b, []byte(shortMessage))
						return shortLength, nil
					}),
				)
			},
			want:    []byte(shortMessage),
			wantErr: false,
		},
		{
			name: "successful read with long message",
			setupMock: func(m *mocks.MockStream) {
				m.EXPECT().SetReadDeadline(gomock.Any()).Return(nil)
				gomock.InOrder(
					// assert that the length of the message is read first
					m.EXPECT().Read(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, len(b), 4)
						// return 4 bytes that represent the length of the message
						copy(b, []byte{0, 0, 1, 4}) // 260 = 0x0104
						return 4, nil
					}),
					// then assert that the message itself is read
					m.EXPECT().Read(gomock.Any()).DoAndReturn(func(b []byte) (int, error) {
						assert.Equal(t, len(b), 260)
						copy(b, []byte(longMessage))
						return longLength, nil
					}),
				)
			},
			want: []byte(longMessage),
		},
		{
			name: "error on read deadline",
			setupMock: func(m *mocks.MockStream) {
				m.EXPECT().SetReadDeadline(gomock.Any()).Return(fmt.Errorf("error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStream := mocks.NewMockStream(ctrl)
			tt.setupMock(mockStream)

			got, err := ReadMessageFromStream(mockStream)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
